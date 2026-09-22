package transport

import (
	"context"
	"net/http"
	"net/url"

	"zellij-with-codeagent/internal/codingagent"
)

type ExplainAgentResponse = codingagent.AgentExplanation

type agentExplanationService interface {
	ExplainAgent(context.Context, codingagent.ID) (codingagent.AgentExplanation, error)
}

func (s *Server) handleExplainAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodGet {
		writeAPIError(w, BadRequest("explain requires GET"), http.StatusMethodNotAllowed)
		return
	}
	service, ok := s.service.(agentExplanationService)
	if !ok {
		writeAPIError(w, APIError{Code: CodeRuntimeError, Message: "agent explanation is unavailable"}, http.StatusNotImplemented)
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := service.ExplainAgent(ctx, codingagent.ID(agentID))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (c *Client) ExplainAgent(ctx context.Context, agentID string) (ExplainAgentResponse, error) {
	var response ExplainAgentResponse
	err := c.do(ctx, http.MethodGet, "/v1/agents/"+url.PathEscape(agentID)+"/explain", nil, &response)
	return response, err
}
