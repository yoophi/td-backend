#!/usr/bin/env python3
"""Build the proposed backend and td-serve contracts from a pinned model snapshot."""
import copy
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
S = json.loads((ROOT / 'api/model-schemas.json').read_text())
def ref(name): return {'$ref': '#/components/schemas/' + name}
def string(**kw): return dict(type='string', **kw)
def integer(**kw): return dict(type='integer', **kw)
def boolean(**kw): return dict(type='boolean', **kw)
def array(item): return {'type': 'array', 'items': item}
def obj(props, required=(), **kw):
    out = dict(type='object', properties=props, **kw)
    if required: out['required'] = list(required)
    return out
def request(props, required=(), **kw): return obj(props, required, additionalProperties=False, **kw)
def nullable_ref(name): return {'type': 'object', 'allOf': [ref(name)], 'nullable': True}
def envelope(data): return obj({'ok': boolean(enum=[True]), 'data': data}, ['ok', 'data'])

S['ProjectID'] = string(
    minLength=40, maxLength=40,
    pattern=r'^prj_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
    example='prj_019a7a6e-8c00-7d42-9f23-8c5e7a1b6d90',
    description='Immutable server-generated prj_ + lowercase canonical UUIDv7 (RFC 9562). Never derived from project name, Git remote or local path. Generated once on project creation; retry with same Idempotency-Key returns the same ID. Treat as opaque and authorize every access.')
S['OperationKey'] = string(
    minLength=36, maxLength=36,
    pattern=r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
    example='f47ac10b-58cc-4372-a567-0e02b2c3d479',
    description='Client-generated lowercase canonical UUIDv4. One key per command intent, including td init; persist and reuse exactly the same key/request for retries. Scoped by Actor and Project, or Actor + @server for project creation.')

S['Status'] = string(enum=['open', 'in_progress', 'blocked', 'in_review', 'closed'])
S['IssueType'] = string(enum=['bug', 'feature', 'task', 'epic', 'chore'])
S['Priority'] = string(enum=['P0', 'P1', 'P2', 'P3', 'P4'])
S['ReviewPolicy'] = string(enum=['trusted', 'delegated', 'strict', 'balanced'], default='trusted')
S['LogType'] = string(enum=['progress', 'security', 'blocker', 'decision', 'hypothesis', 'tried', 'result', 'orchestration'])
S['Points'] = integer(enum=[0, 1, 2, 3, 5, 8, 13, 21])
S['IssueDTO']['properties'].update(status=ref('Status'), type=ref('IssueType'), priority=ref('Priority'), points=ref('Points'))
S['IssueDTO']['description'] = 'Complete store record; revision is opaque. Arrays are never null. Optional reviews require with=reviews. List responses retain description and acceptance for Reader compatibility.'
S['IssueDTO']['properties']['revision'] = string(minLength=1, description='Opaque revision. Strong ETag is this value enclosed in double quotes.')
S['BoardDTO']['properties']['revision'] = string(minLength=1)
S['BoardDTO']['required'].append('revision')
S['BoardDTO']['properties']['view_mode'] = string(enum=['swimlanes', 'backlog'])
S['IssueFile']['properties']['role'] = string(enum=['implementation', 'test', 'reference', 'config'])
S['Activity']['properties']['log_type'] = ref('LogType')
S['LogDTO']['properties']['type'] = ref('LogType')
S['Project'] = obj({'id': string(), 'name': string(), 'repository': string(description='Canonical repository identity, optional; never use an absolute local path as shared identity.'), 'review_policy_mode': ref('ReviewPolicy'), 'title_min_length': integer(default=15, minimum=1), 'title_max_length': integer(default=200, minimum=1), 'created_at': string(format='date-time'), 'capabilities': array(string())}, ['id', 'name', 'review_policy_mode', 'title_min_length', 'title_max_length', 'capabilities'])
S['ProjectCreate'] = request({'name': string(minLength=1), 'repository': string(), 'review_policy_mode': ref('ReviewPolicy'), 'title_min_length': integer(minimum=1, default=15), 'title_max_length': integer(minimum=1, default=200)}, ['name'])
S['Project']['properties']['id'] = {'allOf':[ref('ProjectID')], 'readOnly':True}
fields = {'title': string(minLength=1, description='Trimmed Unicode rune count is validated against project limits, default 15..200.'), 'description': string(), 'acceptance': string(), 'type': ref('IssueType'), 'priority': ref('Priority'), 'points': ref('Points'), 'labels': array(string()), 'parent_id': string(description='Empty string clears the parent.'), 'sprint': string(), 'minor': boolean(), 'due_date': string(description='YYYY-MM-DD or empty string to clear.', pattern=r'^(\d{4}-\d{2}-\d{2})?$'), 'defer_until': string(description='YYYY-MM-DD or empty string to clear.', pattern=r'^(\d{4}-\d{2}-\d{2})?$')}
S['IssueCreate'] = request(dict(fields, created_branch=string(), depends_on=array(string())), ['title'], description='Server assigns ID, timestamps, creator session and open status. Default type task, priority P2, points 0. Dependencies are created atomically.')
S['IssuePatch'] = request(fields, minProperties=1, description='Omitted fields stay unchanged; null is rejected. Clear strings with "" and labels with []. Status, attribution and timestamps require dedicated actions. Substantive edits supersede active review atomically.')
S['IssueDetail'] = obj({'issue': ref('IssueDTO'), 'logs': array(ref('LogDTO')), 'comments': array(ref('CommentDTO')), 'latest_handoff': nullable_ref('HandoffDTO'), 'children': array(ref('IssueDTO')), 'dependencies': array(ref('DependencyDTO')), 'blocked_by': array(ref('DependencyDTO')), 'files': array(ref('IssueFile'))}, ['issue', 'logs', 'comments', 'latest_handoff', 'children', 'dependencies', 'blocked_by', 'files'])
S['IssueList'] = obj({'issues': array(ref('IssueDTO')), 'total': integer(minimum=0), 'limit': integer(), 'offset': integer(), 'has_more': boolean(), 'change_token': string()}, ['issues', 'total', 'limit', 'offset', 'has_more', 'change_token'])
S['TransitionInput'] = request({'reason': string(), 'force': boolean(default=False), 'reviewed_by': string(maxLength=120), 'self_review': boolean(default=False), 'record_only': boolean(default=False), 'admin_reason': string(), 'self_close_exception': string(), 'snapshot': ref('SnapshotInput')}, description='reviewed_by and self_review=true are mutually exclusive. Self review requires a nonempty reason. Server checks project policy and authenticated actor privileges; force never bypasses review policy.')
S['TransitionResult'] = obj({'issue': ref('IssueDTO'), 'noop': boolean(), 'parent_status_updates': array(ref('IssueDTO')), 'descendant_status_updates': array(ref('IssueDTO')), 'auto_unblocked': array(ref('IssueDTO'))}, ['issue', 'noop', 'parent_status_updates', 'auto_unblocked'])
S['ReviewInput'] = request({'decision': string(enum=['approved', 'changes_requested']), 'summary': string(minLength=1), 'reviewed_by': string(maxLength=120), 'self_review': boolean(default=False)}, ['decision', 'summary'])
S['SnapshotInput'] = request({'commit_sha': string(), 'branch': string(), 'dirty_files': integer(minimum=0)})
handoff = {k: array(string()) for k in ['done', 'remaining', 'decisions', 'uncertain']}
S['HandoffInput'] = request(dict(handoff, snapshot=ref('SnapshotInput'), submit_review=boolean(default=True)), description='Creates a handoff and, for an in_progress issue with submit_review=true, submits review in the same transaction.')
S['LogInput'] = request({'message': string(minLength=1), 'type': ref('LogType'), 'work_session_id': string()}, ['message', 'type'])
S['ActivityInput'] = request(dict(handoff, kind=string(enum=['comment', 'log', 'handoff']), message=string(), log_type=ref('LogType'), work_session_id=string(), snapshot=ref('SnapshotInput')), ['kind'], description='comment/log require nonempty message; log requires log_type. Attribution, IDs and timestamps are assigned by server. Passive append never submits review.')
S['BoardCreate'] = request({'name': string(minLength=1), 'query': string(), 'view_mode': string(enum=['swimlanes', 'backlog'], default='swimlanes')}, ['name', 'query'])
S['BoardPatch'] = request({'name': string(minLength=1), 'query': string(), 'view_mode': string(enum=['swimlanes', 'backlog'])}, minProperties=1)
S['BoardCard'] = obj({'board_id': string(), 'position': integer(), 'has_position': boolean(), 'category': string(), 'issue': ref('IssueDTO')}, ['board_id', 'position', 'has_position', 'category', 'issue'])
S['BoardDetail'] = obj({'board': ref('BoardDTO'), 'issues': array(ref('BoardCard'))}, ['board', 'issues'])
S['SessionCreate'] = request({'name': string(), 'branch': string(), 'agent_type': string(), 'context_id': string(minLength=1), 'previous_session_id': string()}, ['context_id'], description='Assigns a project-scoped session ID to the authenticated actor. Local process/worktree state stays on client. Session IDs are attribution, never proof of reviewer independence.')
S['Operation'] = obj({'id': string(), 'session_id': string(), 'method': string(), 'path': string(), 'status': string(enum=['committed']), 'response_status': integer(), 'response': obj({}, additionalProperties=True), 'created_at': string(format='date-time')}, ['id', 'session_id', 'method', 'path', 'status', 'response_status', 'response', 'created_at'], description='Durable receipt atomically committed with mutation. A GET 404 means no committed receipt was visible at that instant; an in-flight request may still commit.')
S['Operation']['properties']['id'] = ref('OperationKey')
S['Error'] = obj({'ok': boolean(enum=[False]), 'error': obj({'code': string(), 'message': string(), 'details': obj({'fields': array(obj({'field': string(), 'rule': string(), 'message': string(), 'value': {}, 'expected': {}}, ['field', 'rule', 'message'])), 'operation_id': string(), 'current_revision': string()}, additionalProperties=True)}, ['code', 'message'])}, ['ok', 'error'])
S['MonitorResult'] = obj({'monitor': ref('MonitorDTO'), 'session_id': string(), 'change_token': string()}, ['monitor', 'session_id', 'change_token'])
S['RefreshEvent'] = obj({'change_token': string(), 'timestamp': string(format='date-time')}, ['change_token', 'timestamp'])
S['PingEvent'] = obj({'change_token': string()}, ['change_token'])

PARAMS = {
 'Session': {'name': 'X-TD-Session', 'in': 'header', 'required': True, 'schema': string(minLength=1), 'description': 'Registered project session owned by authenticated actor. td serve supplies its own web session; browser never needs this header.'},
 'Operation': {'name': 'Idempotency-Key', 'in': 'header', 'required': True, 'schema': ref('OperationKey'), 'description': 'Client generated UUIDv4, retained for project lifetime. Scoped by actor and project, fingerprint includes session, method, path, body and If-Match. Same key+request returns original committed response before revision checks; different request returns 409.'},
 'Revision': {'name': 'If-Match', 'in': 'header', 'required': True, 'schema': string(), 'example': '"r-42"', 'description': 'Exactly one strong ETag for the aggregate being mutated. Wildcard and weak tags rejected. Missing 428, stale 412. Check and mutation are atomic.'},
}
def p(name, schema, description='', **kw): return dict(name=name, schema=schema, description=description, **{'in':'query'}, **kw)
pagination = [p('limit', integer(default=200, minimum=1, maximum=1000)), p('offset', integer(default=0, minimum=0))]
filters = [p('include_closed', boolean(default=False)), p('status', array(ref('Status')), style='form', explode=True), p('type', array(ref('IssueType')), style='form', explode=True), p('priority', array(ref('Priority')), style='form', explode=True), p('labels', array(string()), style='form', explode=True), p('epic', string()), p('parent_id', string()), p('search', string()), p('search_mode', string(enum=['auto','text','tdq'], default='auto')), p('sort', string(enum=['priority','created','updated','id','title','status','type','points'], default='priority')), p('order', string(enum=['asc','desc'], default='asc'))]
paths = {}
def endpoint(path, method, opid, tag, summary, data, body=None, status=200, params=(), revision=False, source='proposed', description='', public=False):
    mut = method not in ['get', 'head']
    parameters = [{'name': n, 'in': 'path', 'required': True, 'schema': ref('ProjectID') if n=='project_id' else ref('OperationKey') if n=='operation_id' else string(minLength=1)} for n in re.findall(r'{(.*?)}', path)]
    if '{project_id}' in path: parameters.append({'$ref':'#/components/parameters/Session'})
    if mut: parameters.append({'$ref':'#/components/parameters/Operation'})
    if revision: parameters.append({'$ref':'#/components/parameters/Revision'})
    parameters += list(params)
    response = {'description': 'Success', 'content': {'application/json': {'schema': envelope(data)}}}
    if revision or (method=='get' and path.endswith(('{issue_id}', '{board_id}', '{note_id}'))):
        response['headers']={'ETag': {'schema': string(), 'description':'Strong quoted aggregate revision.'}}
    responses = {str(status): response}
    for code in ['400','401','403','404','409','500','503']:
        responses[code] = {'$ref':'#/components/responses/Error'+code}
    if revision:
        for code in ['412','428']: responses[code]={'$ref':'#/components/responses/Error'+code}
    op = dict(operationId=opid, tags=[tag], summary=summary, description=description or summary, responses=responses, **{'x-origin':source})
    if parameters: op['parameters']=parameters
    if body: op['requestBody']={'required':True, 'content':{'application/json':{'schema':body}}}
    if public: op['security']=[]
    paths.setdefault(path,{})[method]=op

base='/v1/projects/{project_id}'
issues=base+'/issues'
issue=issues+'/{issue_id}'
boards=base+'/boards'
board=boards+'/{board_id}'
flag=lambda name: obj({name: boolean()}, [name])
one=lambda key,name: obj({key:ref(name)},[key])
many=lambda key,name: obj({key:array(ref(name))},[key])
endpoint('/healthz','get','health','System','Backend health',obj({'status':string(enum=['ok']), 'api_version':string()},['status','api_version']),public=True)
endpoint('/v1/capabilities','get','capabilities','System','Discover supported store capabilities',obj({'api_version':string(), 'capabilities':array(string())},['api_version','capabilities']))
endpoint('/v1/projects','post','createProject','Projects','Create remote project for td init',one('project','Project'),ref('ProjectCreate'),201,description='Always creates a project; same Idempotency-Key recovers the same project. CLI persists returned project ID and init key. Never guess project identity from directory basename.')
endpoint('/v1/projects','get','listProjects','Projects','List accessible projects',many('projects','Project'))
endpoint(base,'get','getProject','Projects','Read project settings',one('project','Project'))
endpoint(base+'/sessions','post','createSession','Sessions','Register CLI or td serve session',one('session','SessionDTO'),ref('SessionCreate'),201)
# Session registration cannot require a session that does not exist yet.
paths[base+'/sessions']['post']['parameters']=[x for x in paths[base+'/sessions']['post']['parameters'] if x.get('$ref')!='#/components/parameters/Session']
endpoint(base+'/sessions','get','listSessions','Sessions','List project sessions',obj({'sessions':array(ref('SessionDTO')), 'current_session_id':string()},['sessions','current_session_id']),source='adapted:serve.HandleListSessions')
endpoint(base+'/sessions/{session_id}/heartbeat','post','heartbeatSession','Sessions','Update own session presence',obj({'session_id':string(),'last_activity':string(format='date-time')},['session_id','last_activity']),description='Empty request body. Only the caller session may heartbeat. Successful new operations update last_activity and project change token; retries replay without refreshing timestamps.')
endpoint(issues,'get','listIssues','Issues','List complete issue records',ref('IssueList'),params=pagination+filters,source='adapted:serve.HandleListIssues',description='Default excludes closed and deleted, includes blocked/in_review. Complete fields unlike existing slim GUI list. All pages carry one change_token; Reader adapter must restart if tokens differ between pages. Stable tie breaker is id. search_mode=auto attempts TDQ and falls back to text only on invalid syntax/fields; tdq reports invalid queries; text searches literal ID/title/description/acceptance. Structured filters intersect with search before pagination. An explicit sort/order overrides TDQ sort; otherwise TDQ sort is preserved. with_deleted=true is for archive/restore only and includes deleted issue query facts.',)
paths[issues]['get']['parameters'].append(p('with_deleted',boolean(default=False)))
endpoint(issues,'post','createIssue','Issues','Create issue',one('issue','IssueDTO'),ref('IssueCreate'),201,source='adapted:serve.HandleCreateIssue')
endpoint(issue,'get','getIssue','Issues','Get issue and related context',ref('IssueDetail'),params=[p('with',string(enum=['reviews'])),p('with_deleted',boolean(default=False))],source='adapted:serve.HandleGetIssue')
endpoint(issue,'patch','updateIssue','Issues','Update observed issue fields',one('issue','IssueDTO'),ref('IssuePatch'),revision=True,source='adapted:serve.HandleUpdateIssue')
endpoint(issue,'delete','deleteIssue','Issues','Soft delete issue',flag('deleted'),revision=True,source='adapted:serve.HandleDeleteIssue')
endpoint(issue+'/restore','post','restoreIssue','Issues','Restore soft deleted issue',one('issue','IssueDTO'),revision=True)
for action in ['start','unstart','review','approve','reject','block','unblock','close','reopen']:
    endpoint(issue+'/'+action,'post',action+'Issue','Workflow',action.capitalize()+' issue',ref('TransitionResult'),ref('TransitionInput'),revision=True,source='proposed' if action=='unstart' else 'adapted:serve.Handle'+action.capitalize(),description='Server applies td workflow and reviewpolicy using full session history. Status, claim, review invalidation, cascades, audit and operation receipt commit atomically. Return parent_status_updates and auto_unblocked even when empty. Review/approve/close must validate latest handoff and review basis; rejected policy leaves no partial state.')
endpoint(issue+'/reviews','post','recordReview','Workflow','Record review without closing',obj({'issue':ref('IssueDTO'),'review':ref('IssueReviewDTO')},['issue','review']),ref('ReviewInput'),201,revision=True,source='adapted:serve.HandleRecordReview')
endpoint(issue+'/status','post','setIssueStatus','Workflow','Apply CLI explicit status change through workflow policy',ref('TransitionResult'),request({'status':ref('Status'),'reason':string(),'force':boolean(default=False)},['status']),revision=True,description='Supports td update --status and td-gui CLI-backed /gui/status. Resolve named transition and validate td workflow/reviewpolicy. Never use generic PATCH or bypass approval/close rules. Unsupported transition returns 409.')
endpoint(issue+'/comments','post','addComment','Activity','Add comment',one('comment','CommentDTO'),request({'text':string(minLength=1)},['text']),201,revision=True,source='adapted:serve.HandleAddComment')
endpoint(issue+'/comments/{comment_id}','delete','deleteComment','Activity','Delete comment',flag('deleted'),revision=True,source='adapted:serve.HandleDeleteComment')
endpoint(issue+'/logs','post','appendLog','Activity','Append CLI progress log',one('log','LogDTO'),ref('LogInput'),201,revision=True)
endpoint(issue+'/handoffs','post','handoffIssue','Activity','Create handoff and optionally submit review',obj({'handoff':ref('HandoffDTO'),'issue':ref('IssueDTO'),'review_requested':boolean()},['handoff','issue','review_requested']),ref('HandoffInput'),201,revision=True)
endpoint(issue+'/activity','get','listActivity','Activity','Read complete shared issue history',many('activity','Activity'))
endpoint(issue+'/activity','post','appendActivity','Activity','Append passive shared activity',one('activity','Activity'),ref('ActivityInput'),201,revision=True)
endpoint(issue+'/dependencies','post','addDependency','Relations','Add dependency',one('dependency','DependencyDTO'),request({'depends_on':string(minLength=1)},['depends_on']),201,revision=True,source='adapted:serve.HandleAddDependency',description='Validate same project, target existence, no self edge and no cycle within transaction. Relation direction: issue_id depends on depends_on_id. Return dep_id used for deletion.')
endpoint(issue+'/dependencies/{dep_id}','delete','removeDependency','Relations','Remove dependency',flag('removed'),revision=True,source='adapted:serve.HandleDeleteDependency')
endpoint(issue+'/files','put','replaceFiles','Relations','Replace linked file metadata',one('issue','IssueDTO'),request({'files':array(request({'file_path':string(minLength=1),'role':S['IssueFile']['properties']['role'],'linked_sha':string()},['file_path','role']))},['files']),revision=True,description='Metadata only; no file contents or server filesystem access. Empty array removes all links.')
endpoint(boards,'get','listBoards','Boards','List shared boards',many('boards','BoardDTO'),source='adapted:serve.HandleListBoards')
endpoint(boards,'post','createBoard','Boards','Create TDQ board',one('board','BoardDTO'),ref('BoardCreate'),201,source='adapted:serve.HandleCreateBoard')
endpoint(board,'get','getBoard','Boards','Read board cards and ordering',ref('BoardDetail'),params=[p('include_closed',boolean(default=False))],source='adapted:serve.HandleGetBoard')
endpoint(board,'patch','updateBoard','Boards','Edit board definition and view mode',one('board','BoardDTO'),ref('BoardPatch'),revision=True,source='adapted:serve.HandleUpdateBoard',description='Builtin query/name are immutable (403); view mode and positions may change. Validate TDQ before commit.')
endpoint(board,'delete','deleteBoard','Boards','Delete non-builtin board',flag('deleted'),revision=True,source='adapted:serve.HandleDeleteBoard')
endpoint(board+'/issues','post','setBoardPosition','Boards','Pin issue at insertion slot',flag('positioned'),request({'issue_id':string(),'position':integer(minimum=1)},['issue_id','position']),revision=True,source='adapted:serve.HandleSetBoardPosition',description='position is a 1-based insertion slot among explicitly positioned cards; stored sparse sort keys are not slots. Compute and persist order atomically.')
endpoint(board+'/move','post','moveBoardIssue','Boards','Move issue before another visible card',flag('positioned'),request({'issue_id':string(),'before_id':string(description='Empty means append.'),'include_closed':boolean(default=False)},['issue_id']),revision=True,source='adapted:serve.HandleMoveBoardIssue',description='Resolve before_id against current visible board order in transaction. This differs from the 1-based slot input of POST /boards/{board_id}/issues.')
endpoint(board+'/issues/{issue_id}','delete','unpinBoardIssue','Boards','Remove explicit board position',flag('removed'),revision=True,source='adapted:serve.HandleRemoveBoardPosition')
endpoint(board+'/visit','post','visitBoard','Boards','Persist last viewed board time',one('board','BoardDTO'),revision=True)
endpoint(base+'/monitor','get','getMonitor','Monitor','Fetch session aware monitor projection',ref('MonitorResult'),params=[p('include_closed',boolean(default=False)),p('search',string()),p('search_mode',string(enum=['auto','text','tdq'],default='auto')),p('sort',string()),p('board_id',string())],source='adapted:serve.HandleMonitor',description='Populate all TaskListDTO categories using td reviewpolicy and dependency queries for the requesting session. Return one consistent snapshot including focus, activity, handoffs, active sessions and token. TUI adapter converts DTO to RefreshDataMsg and resolves selected board separately.')
focus=string(nullable=True)
endpoint(base+'/focus','get','getFocus','Monitor','Read requesting session focus',obj({'focused_issue_id':focus},['focused_issue_id']))
endpoint(base+'/focus','put','setFocus','Monitor','Set requesting session focus',obj({'focused_issue_id':focus},['focused_issue_id']),request({'issue_id':focus},['issue_id']),source='adapted:serve.HandleSetFocus',description='null clears focus. Scoped by project and caller session, never a project global value.')
endpoint(base+'/stats','get','getStats','Monitor','Read project statistics',ref('StatsDTO'),source='adapted:serve.HandleStats')
endpoint(base+'/labels','get','getLabels','Monitor','Read distinct project labels',obj({'labels':array(string()),'workflows':array(obj({})),'default_workflow':string()},['labels','workflows','default_workflow']),source='adapted:serve.HandleListLabels')
endpoint(base+'/changes','get','getChangeToken','Monitor','Poll project change token',obj({'change_token':string()},['change_token']),description='Opaque project token advances on every successful mutation, including comments, board settings, notes and focus. No-op may preserve token. Compare for equality only.')
endpoint(base+'/events','get','streamEvents','Monitor','Stream refresh and ping SSE events',string(),params=[{'name':'Last-Event-ID','in':'header','schema':string()}],source='adapted:serve.SSEHub',description='text/event-stream. id is change_token; event refresh data is RefreshEvent; ping data is PingEvent. Initial/reconnect refresh with current token when cursor differs; no durable delta replay. Client invalidates and refetches. Heartbeat every 15s. td serve relays as /v1/events to browser; fall back to changes polling on disconnect.')
paths[base+'/events']['get']['responses']['200']={'description':'SSE stream; named refresh and ping events.','content':{'text/event-stream':{'schema':string(),'example':'id: c-42\nevent: refresh\ndata: {"change_token":"c-42","timestamp":"2026-10-13T00:00:00Z"}\n\n'}},'x-event-schemas':{'refresh':ref('RefreshEvent'),'ping':ref('PingEvent')}}
endpoint(base+'/query','get','queryIssues','Issues','Evaluate TDQ and return issue IDs',obj({'ids':array(string())},['ids']),params=[p('q',string(),required=True)],description='Execute TDQ against backend snapshot and caller session. Also used by CLI-backed td-gui /gui/query; GUI-only routes remain owned by td-gui.')
notes=base+'/notes'
endpoint(notes,'get','listNotes','Notes','List shared notes',many('notes','Note'))
endpoint(notes,'post','createNote','Notes','Create note',one('note','Note'),request({'title':string(minLength=1),'content':string(),'pinned':boolean(default=False)},['title']),201)
S['Note']['properties']['revision']=string(minLength=1)
S['Note']['required'].append('revision')
endpoint(notes+'/{note_id}','get','getNote','Notes','Read note',one('note','Note'))
endpoint(notes+'/{note_id}','patch','updateNote','Notes','Edit note',one('note','Note'),request({'title':string(minLength=1),'content':string(),'pinned':boolean(),'archived':boolean()},minProperties=1),revision=True)
endpoint(notes+'/{note_id}','delete','deleteNote','Notes','Soft delete note',flag('deleted'),revision=True)
ws=base+'/work-sessions'
endpoint(ws,'get','listWorkSessions','Sessions','List multi-issue work sessions',many('work_sessions','WorkSession'))
endpoint(ws,'post','startWorkSession','Sessions','Start multi-issue work session',one('work_session','WorkSession'),request({'name':string(minLength=1),'start_sha':string()},['name']),201,description='Server records session attribution. Active work session selection, worktree root and repository path stay in client context.')
endpoint(ws+'/{work_session_id}','get','getWorkSession','Sessions','Read work session and tagged issues',obj({'work_session':ref('WorkSession'),'issue_ids':array(string())},['work_session','issue_ids']))
endpoint(ws+'/{work_session_id}/issues','put','tagWorkSessionIssues','Sessions','Replace tagged issue IDs',obj({'issue_ids':array(string())},['issue_ids']),request({'issue_ids':array(string())},['issue_ids']),description='Only owning session may edit an active work session. Validate all issue IDs and replace atomically.')
endpoint(ws+'/{work_session_id}/end','post','endWorkSession','Sessions','End own active work session',one('work_session','WorkSession'),request({'end_sha':string()}),description='Only owning session may end. Idempotency receipt prevents duplicate ending; server assigns ended_at.')
endpoint(base+'/operations/{operation_id}','get','getOperation','History','Inspect committed write receipt',one('operation','Operation'))
endpoint(base+'/operations','get','listOperations','History','List mutation history for td undo selection',many('operations','Operation'),params=pagination+[p('session_id',string()),p('issue_id',string())])
endpoint(base+'/operations/{operation_id}/undo','post','undoOperation','History','Conditionally undo recorded mutation',obj({'undone':boolean(),'affected_issues':array(ref('IssueDTO'))},['undone','affected_issues']),request({'reason':string(minLength=1)},['reason']),description='Backend loads operation after-revisions and checks all affected aggregates atomically. Any later write prevents undo with 409. Inverse, review invalidation, audit and receipt commit atomically; never overwrite subsequent work. Multi-entity cascades are undone as one operation. Revisions and content versions increase; review/history audit rows are preserved and approvals are superseded. Project/session creation, heartbeat, undo-of-undo and unknown effect versions are rejected with 409. Focus/WorkSession inverses require their owning session. Restored dependency/parent cycles reject the whole inverse.')

responses={}
for code,msg in {'400':'Invalid JSON, field or TDQ; validation_error','401':'Authentication required; unauthorized','403':'Project access, builtin restriction or review policy; forbidden','404':'Resource or receipt not found; not_found','409':'Invalid workflow, relation cycle or idempotency fingerprint mismatch; conflict','412':'Stale If-Match; conflict','428':'If-Match required; precondition_required','500':'Internal error; internal','503':'Backend unavailable; store_error'}.items():
    responses['Error'+code]={'description':msg,'content':{'application/json':{'schema':ref('Error')}}}
doc={'openapi':'3.0.3','info':{'title':'td-backend Store API','version':'0.1.0','description':'Proposed API contract for td CLI, td monitor and td serve -> td-gui. Implementation is incremental; consult runtime capabilities and docs/implementation-progress.md for supported operations. The complete contract includes planned operations. Adapted operations reuse domain/response semantics, not wire compatibility with existing project API. All project writes are transactional, revision checked where applicable, and have durable idempotent receipts.'},'servers':[{'url':'http://localhost:7000'}],'security':[{'BearerAuth':[]}],'tags':[{'name':n} for n in ['System','Projects','Sessions','Issues','Workflow','Activity','Relations','Boards','Monitor','Notes','History']],'paths':paths,'components':{'schemas':S,'parameters':PARAMS,'responses':responses,'securitySchemes':{'BearerAuth':{'type':'http','scheme':'bearer','description':'Backend access token supplied by remote adapter. Explicit localhost-only development mode may disable authentication; GUI browsers access local td serve and never receive backend credentials.'}}},'x-source-commit':'c64d7873d8f555dd49875e34e6b39ae5257e2e0c','x-status':'proposed'}
(ROOT/'swagger.json').write_text(json.dumps(doc,ensure_ascii=False,indent=2)+'\n')

# The local serve facade keeps GUI paths and injects project/session/write headers.
local=copy.deepcopy(doc)
local['info']['title']='td serve remote-store facade API'
local['info']['description']='Proposed td serve facade when using td-backend. Preserves td-gui /v1 routes and envelopes. serve selects saved project, registers web session, creates per-request idempotency keys, and loads revisions before mutations. Browser If-Match is optional; without it serve cannot protect stale UI edits. GUI-only /gui/* routes stay in td-gui.'
local['servers']=[{'url':'http://localhost:54321'}]
local['security']=[{'BearerAuth':[]}]
local['components']['securitySchemes']['BearerAuth']['description']='Existing td serve local bearer token, injected by td-gui reverse proxy. This token is distinct from backend credentials and never exposed to browser.'
local['paths']={}
for path,operations in paths.items():
    if not path.startswith(base+'/'): continue
    mapped='/v1'+path[len(base):]
    if mapped.startswith(('/v1/operations','/v1/changes')) or mapped=='/v1/query': continue
    ops=copy.deepcopy(operations)
    for method,op in ops.items():
        op['parameters']=[x for x in op.get('parameters',[]) if x.get('name')!='project_id' and x.get('$ref') not in ['#/components/parameters/Session','#/components/parameters/Operation']]
        op['parameters']=[dict(PARAMS['Revision'],required=False) if x.get('$ref')=='#/components/parameters/Revision' else x for x in op['parameters']]
        op['responses'].pop('428',None)
    local['paths'][mapped]=ops
local['paths']['/health']={'get':{'operationId':'serveHealth','tags':['System'],'summary':'Local serve health','responses':{'200':{'description':'Success','content':{'application/json':{'schema':envelope(obj({'status':string(),'session_id':string(),'change_token':string()},['status','session_id','change_token']))}}}}}}
local['paths']['/health']['get']['security']=[]
local['paths']['/v1/project']={'get':{'operationId':'serveProject','tags':['Projects'],'summary':'Local project metadata and backend capabilities','responses':{'200':{'description':'Success','content':{'application/json':{'schema':envelope(obj({'name':string(),'path':string(),'session_id':string(),'title_min_length':integer(),'title_max_length':integer(),'capabilities':array(string())},['name','path','session_id','title_min_length','title_max_length','capabilities']))}}}}}}
(ROOT/'api/td-serve.swagger.json').write_text(json.dumps(local,ensure_ascii=False,indent=2)+'\n')
print(f'Generated backend: {len(paths)} paths, {sum(len(v) for v in paths.values())} operations; serve: {len(local["paths"])} paths')
