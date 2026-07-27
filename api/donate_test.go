package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopds-api/config"

	"github.com/gin-gonic/gin"
)

// The donate list is the one piece of configuration a reader sees, and the one
// most easily left behind by someone running their own copy of this service.
// Serving nothing when nothing is configured is therefore the important case:
// the alternative is a fork quietly collecting for the original author.

func donateResponse(t *testing.T, methods []config.DonateMethod) (int, []config.DonateMethod) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/donate", http.NoBody)

	DonateMethods(methods)(c)

	var body []config.DonateMethod
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding response %q: %v", recorder.Body.String(), err)
		}
	}
	return recorder.Code, body
}

func TestDonateMethodsServesWhatIsConfigured(t *testing.T) {
	configured := []config.DonateMethod{
		{ID: "bitcoin", Label: "Bitcoin", Kind: "address", Value: "bc1qexample", QR: true},
		{ID: "tinkoff", Label: "Tinkoff", Kind: "card", Value: "5536913994186852", Link: "https://tbank.ru/cf/abc"},
	}

	code, body := donateResponse(t, configured)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body) != 2 {
		t.Fatalf("got %d methods, want 2", len(body))
	}
	if body[0].ID != "bitcoin" || !body[0].QR {
		t.Errorf("first method served wrongly: %+v", body[0])
	}
	if body[1].Link != "https://tbank.ru/cf/abc" {
		t.Errorf("link not served: %q", body[1].Link)
	}
}

func TestDonateMethodsServesOrderAsConfigured(t *testing.T) {
	// The operator decides which way of giving comes first; the list is served
	// in the order they wrote it.
	configured := []config.DonateMethod{
		{ID: "one", Kind: "link", Value: "https://example.test/1"},
		{ID: "two", Kind: "link", Value: "https://example.test/2"},
		{ID: "three", Kind: "link", Value: "https://example.test/3"},
	}

	_, body := donateResponse(t, configured)

	for i, want := range []string{"one", "two", "three"} {
		if body[i].ID != want {
			t.Errorf("position %d is %q, want %q", i, body[i].ID, want)
		}
	}
}

func TestDonateMethodsServesAnEmptyListWhenNoneConfigured(t *testing.T) {
	code, body := donateResponse(t, nil)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body) != 0 {
		t.Errorf("expected nothing to be offered, got %+v", body)
	}
}

func TestDonateMethodsServesAnArrayRatherThanNull(t *testing.T) {
	// A nil slice marshals to null, which the interface would have to guard
	// against separately. An empty array needs no special case.
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/donate", http.NoBody)

	DonateMethods(nil)(c)

	if got := recorder.Body.String(); got != "[]" {
		t.Errorf("body = %q, want %q", got, "[]")
	}
}

// The JSON field names are a contract with the interface, and Go's defaults
// would export them capitalised. An absent link is omitted rather than served
// as an empty string, so the interface can test for its presence.
func TestDonateMethodsServesLowercaseFieldNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/donate", http.NoBody)

	DonateMethods([]config.DonateMethod{
		{ID: "bitcoin", Label: "Bitcoin", Kind: "address", Value: "bc1q", QR: true},
	})(c)

	body := recorder.Body.String()
	for _, want := range []string{`"id":"bitcoin"`, `"label":"Bitcoin"`, `"kind":"address"`, `"value":"bc1q"`, `"qr":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s is missing %s", body, want)
		}
	}
	if strings.Contains(body, `"link"`) {
		t.Errorf("an absent link should be omitted, got %s", body)
	}
}
