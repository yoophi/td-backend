#!/usr/bin/env python3
"""Validate specs and the td-gui facade's required protocol surfaces."""
import json
from pathlib import Path
from openapi_spec_validator import validate

ROOT = Path(__file__).resolve().parents[1]
docs = {}
for filename in ['swagger.json', 'api/td-serve.swagger.json']:
    doc = json.loads((ROOT / filename).read_text())
    validate(doc)
    docs[filename] = doc
    print(f'{filename}: OpenAPI structure and references valid')

local = docs['api/td-serve.swagger.json']
required = {
    '/health': ['get'], '/v1/project': ['get'], '/v1/issues': ['get', 'post'],
    '/v1/issues/{issue_id}': ['get', 'patch', 'delete'],
    '/v1/issues/{issue_id}/reviews': ['post'],
    '/v1/issues/{issue_id}/comments': ['post'],
    '/v1/issues/{issue_id}/comments/{comment_id}': ['delete'],
    '/v1/issues/{issue_id}/dependencies': ['post'],
    '/v1/issues/{issue_id}/dependencies/{dep_id}': ['delete'],
    '/v1/boards': ['get', 'post'], '/v1/boards/{board_id}': ['get', 'patch', 'delete'],
    '/v1/boards/{board_id}/issues': ['post'],
    '/v1/boards/{board_id}/issues/{issue_id}': ['delete'],
    '/v1/focus': ['put'], '/v1/labels': ['get'], '/v1/events': ['get'],
}
for action in ['start', 'review', 'approve', 'reject', 'block', 'unblock', 'close', 'reopen']:
    required[f'/v1/issues/{{issue_id}}/{action}'] = ['post']
for path, methods in required.items():
    for method in methods:
        assert method in local['paths'][path], (method, path)
        for param in local['paths'][path][method].get('parameters', []):
            assert param.get('$ref') not in ['#/components/parameters/Session', '#/components/parameters/Operation']
            assert not (param.get('name') == 'If-Match' and param.get('required')), path

backend = docs['swagger.json']
schemas = backend['components']['schemas']
assert 'status' not in schemas['IssuePatch']['properties']
assert schemas['IssuePatch']['additionalProperties'] is False
assert schemas['ReviewInput']['properties']['reviewed_by']['maxLength'] == 120
assert all(k in schemas['TaskListDTO']['required'] for k in ['ready', 'reviewable', 'pending_review', 'ready_to_close', 'needs_rework'])
assert local['components']['schemas']['IssueDetail']['properties']['comments']['items']['$ref'] == '#/components/schemas/CommentDTO'
comment = local['paths']['/v1/issues/{issue_id}/comments']['post']['requestBody']['content']['application/json']['schema']
assert comment['required'] == ['text']
events = local['paths']['/v1/events']['get']['responses']['200']
assert 'text/event-stream' in events['content']
assert set(events['x-event-schemas']) == {'refresh', 'ping'}
assert local['paths']['/health']['get']['security'] == []
assert local['security'] == [{'BearerAuth': []}]
for path, operations in backend['paths'].items():
    for method, operation in operations.items():
        success = [v for k, v in operation['responses'].items() if k.startswith('2')]
        for response in success:
            if 'application/json' in response.get('content', {}):
                envelope = response['content']['application/json']['schema']
                assert envelope['required'] == ['ok', 'data'], (path, method)
        if method in ['post', 'put', 'patch', 'delete']:
            assert {'$ref': '#/components/parameters/Operation'} in operation['parameters'], (path, method)
assert {'$ref': '#/components/parameters/Session'} not in backend['paths']['/v1/projects/{project_id}/sessions']['post']['parameters']
print(f'GUI contract: {sum(len(v) for v in required.values())} required route/method pairs covered; envelope, SSE, attribution and facade headers verified')
