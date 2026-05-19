
go run ./cmd/agentd serve --socket /tmp/agentd.sock
 curl --unix-socket /tmp/agentd.sock http://localhost/v1/runtime | jq .
 curl --unix-socket /tmp/agentd.sock http://localhost/v1/requests \
    -H 'Content-Type: application/json' \
    -d '{
      "type": "execution_plan",
      "request_id": "req_123",
      "payload": {
        "session": "feature-auth",
        "layout": "triple-horizontal",
        "panes": [
          {"id": "planner", "role": "planner"},
          {"id": "frontend", "role": "react-dev"}
        ]
      }
    }'




 curl --unix-socket /tmp/agentd.sock http://localhost/v1/cleanup 
