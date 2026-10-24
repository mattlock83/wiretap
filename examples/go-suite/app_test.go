package orders

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	wiretap "github.com/mattlock83/wiretap/sdk/go"
)

var existingClient = &http.Client{Timeout: 5 * time.Second}

func TestOrders(t *testing.T) {
	session := wiretap.StartTest(t)
	client := session.HTTPClient(existingClient)

	// User cleanup can still call the application and is captured with this test.
	t.Cleanup(func() {
		response, err := client.Get(session.SUTURL + "/orders/34")
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("cleanup status = %d", response.StatusCode)
		}
	})

	response, err := client.Get(session.SUTURL + "/orders/12")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var order struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		t.Fatal(err)
	}
	if order.ID != 12 || order.Status != "ready" {
		t.Fatalf("unexpected order: %+v", order)
	}
}

func TestRejectsInvalidID(t *testing.T) {
	session := wiretap.StartTest(t)
	client := session.HTTPClient(existingClient)
	response, err := client.Get(session.SUTURL + "/orders/invalid")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	// The invalid request remains validation evidence; the successful assertion
	// keeps this test's framework outcome as passed.
}
