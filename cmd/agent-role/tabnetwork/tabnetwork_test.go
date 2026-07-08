package tabnetwork

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseOptionsDefaults(t *testing.T) {
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions(nil) error = %v", err)
	}

	if opts.Port != 9222 {
		t.Fatalf("Port = %d, want 9222", opts.Port)
	}
	if opts.UserDataDir != defaultUserDataDir {
		t.Fatalf("UserDataDir = %q, want %q", opts.UserDataDir, defaultUserDataDir)
	}
	if opts.LaunchChrome != true {
		t.Fatalf("LaunchChrome = %v, want true", opts.LaunchChrome)
	}
}

func TestParseOptionsAcceptsFiltersAndNoLaunch(t *testing.T) {
	opts, err := parseOptions([]string{
		"--port", "9333",
		"--no-launch",
		"--target-url", "dashboard",
		"--filter-url", "/api/",
		"--method", "post",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if opts.Port != 9333 || opts.LaunchChrome || opts.TargetURL != "dashboard" || opts.Filter.URLContains != "/api/" || opts.Filter.Method != "POST" {
		t.Fatalf("options = %#v, want parsed filters and no launch", opts)
	}
}

func TestChromeArgsUseRemoteDebuggingAndProfile(t *testing.T) {
	got := chromeArgs(9333, "/tmp/profile")
	want := []string{
		"--remote-debugging-port=9333",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chromeArgs() = %#v, want %#v", got, want)
	}
}

func TestRequestStoreDedupesByMethodAndFullURL(t *testing.T) {
	store := newRequestStore()
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Second)

	store.Upsert(networkEvent{
		Kind:       eventRequest,
		Method:     "GET",
		URL:        "https://example.com/api/users?id=1",
		ObservedAt: first,
		RequestID:  "request-1",
		TargetID:   "target-1",
	})
	store.Upsert(networkEvent{
		Kind:        eventResponse,
		Method:      "GET",
		URL:         "https://example.com/api/users?id=1",
		Status:      201,
		ContentType: "application/json",
		ObservedAt:  second,
		RequestID:   "request-2",
		TargetID:    "target-2",
	})

	rows := store.Rows()
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Count != 2 || row.Status != 201 || row.ContentType != "application/json" || row.LastRequestID != "request-2" || row.TargetID != "target-2" {
		t.Fatalf("row = %#v, want merged latest response data", row)
	}
	if !row.FirstSeen.Equal(first) || !row.LastSeen.Equal(second) {
		t.Fatalf("row times = %s/%s, want %s/%s", row.FirstSeen, row.LastSeen, first, second)
	}
}

func TestRequestStoreSeparatesDifferentMethods(t *testing.T) {
	store := newRequestStore()
	now := time.Now()

	store.Upsert(networkEvent{Kind: eventRequest, Method: "GET", URL: "https://example.com/api", ObservedAt: now})
	store.Upsert(networkEvent{Kind: eventRequest, Method: "POST", URL: "https://example.com/api", ObservedAt: now})

	if rows := store.Rows(); len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
}

func TestRequestStoreRowsSortOldestFirst(t *testing.T) {
	store := newRequestStore()
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/newer", ObservedAt: first.Add(time.Second)})
	store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/older", ObservedAt: first})

	rows := store.Rows()
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].URL != "https://example.com/api/older" {
		t.Fatalf("first row URL = %q, want older request first", rows[0].URL)
	}
	if rows[1].URL != "https://example.com/api/newer" {
		t.Fatalf("second row URL = %q, want newer request last", rows[1].URL)
	}
}

func TestRequestStoreCountsRequestAndResponseWithSameRequestIDOnce(t *testing.T) {
	store := newRequestStore()
	now := time.Now()

	store.Upsert(networkEvent{Kind: eventRequest, Method: "GET", URL: "https://example.com/api", RequestID: "same-request", ObservedAt: now})
	store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api", RequestID: "same-request", Status: 200, ObservedAt: now.Add(time.Millisecond)})

	rows := store.Rows()
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Count != 1 {
		t.Fatalf("Count = %d, want 1 for request/response pair", rows[0].Count)
	}
	if rows[0].Status != 200 {
		t.Fatalf("Status = %d, want latest response status 200", rows[0].Status)
	}
}

func TestRequestStoreMergesHeadersAndResponseBody(t *testing.T) {
	store := newRequestStore()
	now := time.Now()

	store.Upsert(networkEvent{
		Kind:           eventRequest,
		Method:         "POST",
		URL:            "https://example.com/api",
		RequestID:      "request-1",
		RequestHeaders: map[string]string{"Authorization": "Bearer test", "Content-Type": "application/json"},
		ObservedAt:     now,
	})
	store.Upsert(networkEvent{
		Kind:            eventResponse,
		Method:          "POST",
		URL:             "https://example.com/api",
		RequestID:       "request-1",
		Status:          201,
		ResponseHeaders: map[string]string{"X-Request-ID": "abc"},
		ResponseBody:    `{"ok":true}`,
		ObservedAt:      now.Add(time.Millisecond),
	})

	rows := store.Rows()
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.RequestHeaders["Authorization"] != "Bearer test" {
		t.Fatalf("RequestHeaders = %#v, want Authorization header", row.RequestHeaders)
	}
	if row.ResponseHeaders["X-Request-ID"] != "abc" {
		t.Fatalf("ResponseHeaders = %#v, want X-Request-ID header", row.ResponseHeaders)
	}
	if row.ResponseBody != `{"ok":true}` {
		t.Fatalf("ResponseBody = %q, want response body", row.ResponseBody)
	}
}

func TestNetworkEventDoesNotFetchResponseBodyBeforeDetail(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})

	updated, _ := model.Update(networkEvent{
		Kind:      eventResponse,
		Method:    "GET",
		URL:       "https://example.com/api",
		RequestID: "request-1",
		TargetID:  "target-1",
		Status:    200,
	})
	next := updated.(trackerModel)

	if len(next.rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(next.rows))
	}
	if next.rows[0].ResponseBody != "" {
		t.Fatalf("ResponseBody = %q, want empty before detail", next.rows[0].ResponseBody)
	}
}

func TestEnteringDetailDoesNotRequestSelectedResponseBody(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{
		Kind:      eventResponse,
		Method:    "GET",
		URL:       "https://example.com/api",
		RequestID: "request-1",
		TargetID:  "target-1",
		Status:    200,
	})
	model.syncRows()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	if !detail.DetailMode {
		t.Fatal("DetailMode = false, want true")
	}
	if cmd != nil {
		t.Fatal("entering detail returned a command, want no lazy body request")
	}
}

func TestModelEnterShowsSelectedDetailAndEscReturnsToList(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api", Status: 200, ObservedAt: time.Now()})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	entered := updated.(trackerModel)
	if entered.DetailMode != true {
		t.Fatal("DetailMode = false, want true after enter")
	}

	updated, _ = entered.Update(tea.KeyMsg{Type: tea.KeyEsc})
	exited := updated.(trackerModel)
	if exited.DetailMode != false {
		t.Fatal("DetailMode = true, want false after esc")
	}
}

func TestModelViewUsesWindowHeight(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	resized := updated.(trackerModel)

	lines := strings.Split(strings.TrimRight(resized.View(), "\n"), "\n")
	if len(lines) != 24 {
		t.Fatalf("View line count = %d, want 24", len(lines))
	}
}

func TestModelViewShowsCurrentURLAtTop(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.applyTabEvent(tabEvent{
		Kind:     tabCreated,
		Target:   PageTarget{ID: "target-1", Type: "page", URL: "https://example.com/current"},
		TargetID: "target-1",
	})

	view := model.View()
	if !strings.Contains(view, "current-url=https://example.com/current") {
		t.Fatalf("View() missing current URL: %q", view)
	}
}

func TestDetailViewUsesTwoPanesForRequestHeadersAndCallResult(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 32
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "GET",
		URL:            "https://example.com/api",
		Status:         200,
		RequestHeaders: map[string]string{"Accept": "application/json"},
		ResponseHeaders: map[string]string{
			"Content-Type": "application/json",
		},
		ResponseBody: `{"ok":true}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel).View()

	if !strings.Contains(detail, "REQUEST HEADERS") {
		t.Fatalf("detail view missing request pane: %q", detail)
	}
	if !strings.Contains(detail, "CALL RESULT") {
		t.Fatalf("detail view missing result pane: %q", detail)
	}
	if !strings.Contains(detail, "Accept: application/json") {
		t.Fatalf("detail view missing request header: %q", detail)
	}
	if !strings.Contains(detail, `"ok": true`) {
		t.Fatalf("detail view missing response body: %q", detail)
	}
}

func TestFormatResponseBodyPrettyPrintsJSONWithoutTruncation(t *testing.T) {
	body := `{"items":[{"id":1},{"id":2}],"tail":"last-value"}`

	got := formatResponseBody(body)

	if !strings.Contains(got, "{\n") {
		t.Fatalf("formatted body is not pretty JSON: %q", got)
	}
	if !strings.Contains(got, `  "tail": "last-value"`) {
		t.Fatalf("formatted body missing tail value: %q", got)
	}
	if strings.Contains(got, "...") {
		t.Fatalf("formatted body is truncated: %q", got)
	}
}

func TestDetailViewCanScrollToFullResponseBody(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 12
	model.store.Upsert(networkEvent{
		Kind:         eventResponse,
		Method:       "GET",
		URL:          "https://example.com/api",
		Status:       200,
		ResponseBody: `{"lines":["line-00","line-01","line-02","line-03","line-04","line-05","line-06","line-07","line-08","line-09"],"tail":"last-value"}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	firstPage := detail.View()
	if strings.Contains(firstPage, "last-value") {
		t.Fatalf("first detail page unexpectedly contains tail before scrolling: %q", firstPage)
	}

	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	bottom := updated.(trackerModel)
	lastPage := bottom.View()
	if !strings.Contains(lastPage, "last-value") {
		t.Fatalf("bottom detail page missing tail value: %q", lastPage)
	}
}

func TestDetailViewScrollsWithJAndK(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 12
	model.store.Upsert(networkEvent{
		Kind:         eventResponse,
		Method:       "GET",
		URL:          "https://example.com/api",
		Status:       200,
		ResponseBody: `{"lines":["line-00","line-01","line-02","line-03","line-04","line-05","line-06","line-07","line-08","line-09"],"tail":"last-value"}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	before := detail.View()

	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	scrolled := updated.(trackerModel)
	afterJ := scrolled.View()
	if scrolled.detailScroll != 1 {
		t.Fatalf("detailScroll after j = %d, want 1", scrolled.detailScroll)
	}
	if afterJ == before {
		t.Fatal("detail view did not change after j")
	}
	if !strings.Contains(afterJ, "detail-scroll 2-") {
		t.Fatalf("detail view missing updated scroll indicator: %q", afterJ)
	}

	updated, _ = scrolled.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	back := updated.(trackerModel)
	if back.detailScroll != 0 {
		t.Fatalf("detailScroll after k = %d, want 0", back.detailScroll)
	}
}

func TestDetailViewFocusesIndependentPanesWithOneAndTwo(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 12
	model.store.Upsert(networkEvent{
		Kind:   eventResponse,
		Method: "POST",
		URL:    "https://example.com/api",
		Status: 500,
		RequestHeaders: map[string]string{
			"X-A": "a",
			"X-B": "b",
			"X-C": "c",
			"X-D": "d",
			"X-E": "e",
		},
		ResponseBody: `{"lines":["line-00","line-01","line-02","line-03","line-04","line-05","line-06","line-07","line-08","line-09"],"tail":"last-value"}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	if detail.detailPane != detailPaneRequest {
		t.Fatalf("detailPane after enter = %v, want request pane", detail.detailPane)
	}

	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	resultFocused := updated.(trackerModel)
	if resultFocused.detailPane != detailPaneResult {
		t.Fatalf("detailPane after 2 = %v, want result pane", resultFocused.detailPane)
	}

	updated, _ = resultFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	resultScrolled := updated.(trackerModel)
	if resultScrolled.detailRightScroll != 1 {
		t.Fatalf("detailRightScroll after j = %d, want 1", resultScrolled.detailRightScroll)
	}
	if resultScrolled.detailLeftScroll != 0 {
		t.Fatalf("detailLeftScroll after right-pane j = %d, want 0", resultScrolled.detailLeftScroll)
	}

	updated, _ = resultScrolled.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	requestFocused := updated.(trackerModel)
	if requestFocused.detailPane != detailPaneRequest {
		t.Fatalf("detailPane after 1 = %v, want request pane", requestFocused.detailPane)
	}

	updated, _ = requestFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	requestScrolled := updated.(trackerModel)
	if requestScrolled.detailLeftScroll != 1 {
		t.Fatalf("detailLeftScroll after request-pane j = %d, want 1", requestScrolled.detailLeftScroll)
	}
	if requestScrolled.detailRightScroll != 1 {
		t.Fatalf("detailRightScroll after request-pane j = %d, want preserved 1", requestScrolled.detailRightScroll)
	}
}

func TestEKeyLoopsThroughErrorAPIs(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/ok", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/bad", Status: 500, ObservedAt: first.Add(time.Second)})
	model.store.Upsert(networkEvent{Kind: eventFailure, Method: "GET", URL: "https://example.com/api/failed", ErrorText: "net::ERR_FAILED", ObservedAt: first.Add(2 * time.Second)})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	firstError := updated.(trackerModel)
	if firstError.rows[firstError.selected].URL != "https://example.com/api/bad" {
		t.Fatalf("selected after first e = %q, want first error API", firstError.rows[firstError.selected].URL)
	}

	updated, _ = firstError.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	secondError := updated.(trackerModel)
	if secondError.rows[secondError.selected].URL != "https://example.com/api/failed" {
		t.Fatalf("selected after second e = %q, want second error API", secondError.rows[secondError.selected].URL)
	}

	updated, _ = secondError.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	wrapped := updated.(trackerModel)
	if wrapped.rows[wrapped.selected].URL != "https://example.com/api/bad" {
		t.Fatalf("selected after wrapped e = %q, want first error API", wrapped.rows[wrapped.selected].URL)
	}
}

func TestFKeyFiltersRowsInRealtime(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/users", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "POST", URL: "https://example.com/api/orders", Status: 201, ObservedAt: first.Add(time.Second)})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	filtering := updated.(trackerModel)
	if !filtering.filterInputActive {
		t.Fatal("filterInputActive = false, want true after f")
	}

	updated, _ = filtering.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("orders")})
	filtered := updated.(trackerModel)
	if filtered.uiFilter != "orders" {
		t.Fatalf("uiFilter = %q, want orders", filtered.uiFilter)
	}
	if len(filtered.rows) != 1 || filtered.rows[0].URL != "https://example.com/api/orders" {
		t.Fatalf("filtered rows = %#v, want only orders API", filtered.rows)
	}

	updated, _ = filtered.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	lessFiltered := updated.(trackerModel)
	if lessFiltered.uiFilter != "order" {
		t.Fatalf("uiFilter after backspace = %q, want order", lessFiltered.uiFilter)
	}

	updated, _ = lessFiltered.Update(tea.KeyMsg{Type: tea.KeyEsc})
	exited := updated.(trackerModel)
	if exited.filterInputActive {
		t.Fatal("filterInputActive = true, want false after esc")
	}
}

func TestListViewMarksErrorAPIsForRedRendering(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/bad", Status: 500, ObservedAt: time.Now()})
	model.syncRows()

	view := model.View()

	if !strings.Contains(view, "ERR") {
		t.Fatalf("View() missing error API marker used by red rendering: %q", view)
	}
}

func TestModelLShowsDetailAndHReturnsToList(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api", Status: 200, ObservedAt: time.Now()})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	entered := updated.(trackerModel)
	if entered.DetailMode != true {
		t.Fatal("DetailMode = false, want true after l")
	}

	updated, _ = entered.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	exited := updated.(trackerModel)
	if exited.DetailMode != false {
		t.Fatal("DetailMode = true, want false after h")
	}
}

func TestModelKeepsFocusedAPIWhenEventsReorderRows(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/older", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/newer", Status: 200, ObservedAt: first.Add(time.Second)})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	focused := updated.(trackerModel)
	if focused.rows[focused.selected].URL != "https://example.com/api/newer" {
		t.Fatalf("selected URL after j = %q, want newer API", focused.rows[focused.selected].URL)
	}

	updated, _ = focused.Update(networkEvent{
		Kind:       eventResponse,
		Method:     "GET",
		URL:        "https://example.com/api/newer",
		Status:     204,
		ObservedAt: first.Add(2 * time.Second),
	})
	reordered := updated.(trackerModel)

	if reordered.rows[reordered.selected].URL != "https://example.com/api/newer" {
		t.Fatalf("selected URL after reorder = %q, want focus to stay on newer API", reordered.rows[reordered.selected].URL)
	}
}

func TestModelGJumpsToBottomAndGGJumpsToTop(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.listHeight = 3
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		model.store.Upsert(networkEvent{
			Kind:       eventResponse,
			Method:     "GET",
			URL:        "https://example.com/api/" + string(rune('a'+i)),
			Status:     200,
			ObservedAt: first.Add(time.Duration(6-i) * time.Second),
		})
	}
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	bottom := updated.(trackerModel)
	if bottom.selected != len(bottom.rows)-1 {
		t.Fatalf("selected after G = %d, want %d", bottom.selected, len(bottom.rows)-1)
	}
	if bottom.scroll != 3 {
		t.Fatalf("scroll after G = %d, want 3", bottom.scroll)
	}

	updated, _ = bottom.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	pending := updated.(trackerModel)
	if pending.selected != bottom.selected {
		t.Fatalf("selected after first g = %d, want unchanged %d", pending.selected, bottom.selected)
	}

	updated, _ = pending.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	top := updated.(trackerModel)
	if top.selected != 0 {
		t.Fatalf("selected after gg = %d, want 0", top.selected)
	}
	if top.scroll != 0 {
		t.Fatalf("scroll after gg = %d, want 0", top.scroll)
	}
}

func TestModelScrollsToKeepFocusedAPIVisible(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.listHeight = 3
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		model.store.Upsert(networkEvent{
			Kind:       eventResponse,
			Method:     "GET",
			URL:        "https://example.com/api/" + string(rune('a'+i)),
			Status:     200,
			ObservedAt: first.Add(time.Duration(6-i) * time.Second),
		})
	}
	model.syncRows()

	var updated tea.Model = model
	for i := 0; i < 5; i++ {
		updated, _ = updated.(trackerModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	scrolled := updated.(trackerModel)

	if scrolled.selected != 5 {
		t.Fatalf("selected = %d, want 5", scrolled.selected)
	}
	if scrolled.scroll != 3 {
		t.Fatalf("scroll = %d, want 3 to keep selected row visible", scrolled.scroll)
	}

	visible := scrolled.visibleRows()
	if len(visible) != 3 {
		t.Fatalf("len(visible) = %d, want 3", len(visible))
	}
	if visible[len(visible)-1].key() != scrolled.rows[scrolled.selected].key() {
		t.Fatalf("focused row is not visible: visible=%#v focused=%#v", visible, scrolled.rows[scrolled.selected])
	}
}
