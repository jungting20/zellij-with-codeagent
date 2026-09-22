package transport

import (
	"context"
	"net/http"
	"net/url"

	"zellij-with-codeagent/internal/followup"
)

type FollowupService interface {
	Get(context.Context, string) (followup.Queue, error)
	Update(context.Context, string, followup.Update) (followup.Queue, error)
	Summary(string) (int, bool, bool)
}

type AgentFollowupsResponse struct {
	Queue followup.Queue `json:"queue"`
}
type UpdateAgentFollowupsRequest = followup.Update

func (s *Server) handleAgentFollowups(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeAPIError(w, BadRequest("followups requires GET or POST"), http.StatusMethodNotAllowed)
		return
	}
	if s.followups == nil {
		writeAPIError(w, APIError{Code: CodeRuntimeError, Message: "follow-up queues unavailable; restart the daemon with the updated binary"}, http.StatusNotImplemented)
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	var q followup.Queue
	var err error
	if r.Method == http.MethodGet {
		q, err = s.followups.Get(ctx, id)
	} else {
		var req UpdateAgentFollowupsRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		q, err = s.followups.Update(ctx, id, req)
	}
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AgentFollowupsResponse{Queue: q})
}

func (c *Client) GetAgentFollowups(ctx context.Context, id string) (AgentFollowupsResponse, error) {
	var response AgentFollowupsResponse
	err := c.do(ctx, http.MethodGet, "/v1/agents/"+url.PathEscape(id)+"/followups", nil, &response)
	return response, err
}

func (c *Client) UpdateAgentFollowups(ctx context.Context, id string, req UpdateAgentFollowupsRequest) (AgentFollowupsResponse, error) {
	var response AgentFollowupsResponse
	err := c.do(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(id)+"/followups", req, &response)
	return response, err
}
