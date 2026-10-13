package undo

import (
	"fmt"

	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func prepareIssue(tx port.TxScope, c domain.Caller, effect port.Effect, key, reason string) (plan, error) {
	if effect.StateVersion < 1 || effect.StateVersion > 3 {
		return nil, domain.ErrConflict
	}
	current, e := tx.Issues().Get(tx.Context(), c.ProjectID, effect.EntityID, true)
	if e != nil {
		return nil, e
	}
	if e = observed(effect, current.Revision); e != nil {
		return nil, e
	}
	deps, e := tx.Issues().Dependencies(tx.Context(), c.ProjectID, current.ID)
	if e != nil {
		return nil, e
	}
	files, e := tx.Issues().Files(tx.Context(), c.ProjectID, current.ID)
	if e != nil {
		return nil, e
	}
	before := issue.State{Issue: *current, Dependencies: deps, Files: files}
	target := before
	if effect.BeforeJSON != nil {
		target = issue.State{}
		if e = decode(effect.BeforeJSON, &target); e != nil {
			return nil, e
		}
		if target.Issue.ID != current.ID {
			return nil, domain.ErrConflict
		}
		// Version 1 did not record files. Preserve them rather than clearing data
		// outside that snapshot's format; all newer inverses record full files.
		if effect.StateVersion == 1 {
			target.Files = files
		}
	}
	for _, dep := range target.Dependencies {
		if dep.IssueID != current.ID {
			return nil, domain.ErrConflict
		}
	}
	for _, file := range target.Files {
		if file.IssueID != current.ID {
			return nil, domain.ErrConflict
		}
	}
	deltaBefore := map[string]port.Activity{}
	deltaCurrent := []port.Activity{}
	if effect.StateVersion == 3 {
		var recordedAfter issue.State
		if e = decode(effect.AfterJSON, &recordedAfter); e != nil {
			return nil, e
		}
		live, e := tx.Workflow().Activities(tx.Context(), c.ProjectID, current.ID, true)
		if e != nil {
			return nil, e
		}
		byID := map[string]port.Activity{}
		for _, a := range live {
			byID[a.ID] = a
		}
		if effect.BeforeJSON != nil {
			for _, a := range target.ActivityChanges {
				if a.IssueID != current.ID {
					return nil, domain.ErrConflict
				}
				deltaBefore[a.ID] = a
			}
		}
		seen := map[string]bool{}
		for _, a := range recordedAfter.ActivityChanges {
			if a.IssueID != current.ID || seen[a.ID] {
				return nil, domain.ErrConflict
			}
			seen[a.ID] = true
			v, ok := byID[a.ID]
			if !ok {
				return nil, domain.ErrConflict
			}
			deltaCurrent = append(deltaCurrent, v)
		}
		for id := range deltaBefore {
			if !seen[id] {
				return nil, domain.ErrConflict
			}
		}
	}
	return func(command operation.CommandContext) (*domain.Issue, error) {
		if effect.BeforeJSON == nil {
			target.Issue.DeletedAt = &command.Now
		}
		target.Issue.Revision = current.Revision + 1
		target.Issue.ContentVersion = current.ContentVersion + 1
		target.Issue.UpdatedAt = command.Now
		target.Issue.CreatedAt = current.CreatedAt
		target.Issue.CreatorSession = current.CreatorSession
		// An inverse never revives an attestation against the old content basis.
		target.Issue.ReviewerSession = ""
		target.Issue.ReviewedAt = nil
		if e := command.Tx.IssueWrites().SupersedeReviews(command.Tx.Context(), c.ProjectID, current.ID, command.Now); e != nil {
			return nil, e
		}
		if e := command.Tx.IssueWrites().Save(command.Tx.Context(), c.ProjectID, target.Issue, current.Revision); e != nil {
			return nil, e
		}
		for _, dep := range deps {
			if e := command.Tx.IssueWrites().RemoveDependency(command.Tx.Context(), c.ProjectID, current.ID, dep.ID); e != nil {
				return nil, e
			}
		}
		for _, dep := range target.Dependencies {
			if e := command.Tx.IssueWrites().AddDependency(command.Tx.Context(), c.ProjectID, dep); e != nil {
				return nil, e
			}
		}
		if e := command.Tx.IssueWrites().ReplaceFiles(command.Tx.Context(), c.ProjectID, current.ID, target.Files); e != nil {
			return nil, e
		}
		before.ActivityChanges = append([]port.Activity{}, deltaCurrent...)
		target.ActivityChanges = []port.Activity{}
		for _, a := range deltaCurrent {
			restored, existed := deltaBefore[a.ID]
			if existed {
				a.DeletedAt = restored.DeletedAt
			} else {
				a.DeletedAt = &command.Now
			}
			a.UpdatedAt = command.Now
			if e := command.Tx.WorkflowWrites().SetActivityDeleted(command.Tx.Context(), c.ProjectID, current.ID, a.ID, a.DeletedAt, command.Now); e != nil {
				return nil, e
			}
			target.ActivityChanges = append(target.ActivityChanges, a)
		}
		audit := port.Activity{ID: command.RowID + ":undo:" + current.ID, IssueID: current.ID, SessionID: c.SessionID, OperationRowID: command.RowID, Kind: "log", LogType: domain.LogTypeSecurity, SnapshotEvent: "undo", Message: fmt.Sprintf("Undo %s: %s", key, reason), ContentVersion: target.Issue.ContentVersion, CreatedAt: command.Now, UpdatedAt: command.Now}
		if e := command.Tx.WorkflowWrites().AppendActivity(command.Tx.Context(), c.ProjectID, audit); e != nil {
			return nil, e
		}
		target.ActivityChanges = append(target.ActivityChanges, audit)
		effect.StateVersion = 3
		if e := inverse(command, c, effect, before, target, target.Issue.Revision); e != nil {
			return nil, e
		}
		return &target.Issue, nil
	}, nil
}
