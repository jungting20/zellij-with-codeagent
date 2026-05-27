package transport

import (
	"net/http"
	"strings"

	rt "zellij-with-codeagent/internal/runtime"
)

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.ListSessions(ctx)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, SessionListResponse{Sessions: SessionsFromRuntime(response)})
}

func (s *Server) handleSessionRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, BadRequest("only GET is supported for session routes"), http.StatusMethodNotAllowed)
		return
	}

	sessionID, tabID, subResource, ok := splitSessionPath(r.URL.Path)
	if !ok || sessionID == "" {
		writeAPIError(w, BadRequest("invalid session path"), http.StatusBadRequest)
		return
	}

	ctx, cancel := s.requestContext(r)
	defer cancel()

	if tabID == "" && subResource == "" {
		session, err := s.service.GetSession(ctx, rt.SessionID(sessionID))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, SessionResponse{Session: SessionFromRuntime(session)})
		return
	}

	if tabID == "tabs" && subResource == "" {
		tabs, err := s.service.ListTabs(ctx, rt.SessionID(sessionID))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, TabListResponse{Tabs: TabsFromRuntime(tabs)})
		return
	}

	if tabID == "tabs" && subResource != "" {
		parts := strings.Split(subResource, "/")
		actualTabID := parts[0]

		if len(parts) == 1 {
			tab, err := s.service.GetTab(ctx, rt.SessionID(sessionID), rt.TabID(actualTabID))
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, TabResponse{Tab: TabFromRuntime(tab)})
			return
		}

		if len(parts) == 2 && parts[1] == "panes" {
			tab, err := s.service.GetTab(ctx, rt.SessionID(sessionID), rt.TabID(actualTabID))
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, ListPanesResponse{Panes: PanesFromRuntimeRecords(tab.Panes)})
			return
		}
	}

	writeAPIError(w, APIError{Code: CodeNotFound, Message: "route not found"}, http.StatusNotFound)
}

func splitSessionPath(path string) (sessionID, tabID, subResource string, ok bool) {
	rest := strings.TrimPrefix(path, "/v1/sessions/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", "", false
	}
	sessionID = parts[0]
	if len(parts) > 1 {
		tabID = parts[1]
	}
	if len(parts) > 2 {
		subResource = strings.Join(parts[2:], "/")
	}
	return sessionID, tabID, subResource, true
}
