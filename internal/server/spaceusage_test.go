package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The size of a space was computed for local quota warnings and never reported
// over the API, so a server operator — and anything metering a fleet of them —
// had no way to ask how big a space was without shelling into the machine.
func TestSpaceAPIReportsItsSize(t *testing.T) {
	ts, token := scopedFixture(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/spaces/alpha", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET space returned %d", res.StatusCode)
	}

	var got struct {
		Name  string `json:"name"`
		Bytes int64  `json:"bytes"`
		Files int    `json:"files"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "alpha" {
		t.Fatalf("got space %q", got.Name)
	}
	// A space seeded from solo-default is not empty, so zero here means the
	// numbers are not being computed rather than that the space is small.
	if got.Files <= 0 {
		t.Errorf("a seeded space reports %d files", got.Files)
	}
	if got.Bytes <= 0 {
		t.Errorf("a seeded space reports %d bytes", got.Bytes)
	}
}
