package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestDeriveFeesUsesSkillsAndNPCStandings(t *testing.T) {
	station := eveStation{CorporationID: 1, FactionID: 2}
	skills := eveSkills{Skills: []eveSkill{{SkillID: accountingID, CurrentLevel: 5}, {SkillID: brokerSkillID, CurrentLevel: 5}}}
	standings := []eveStanding{{FromID: 1, FromType: "npc_corp", Standing: 10}, {FromID: 2, FromType: "faction", Standing: 10}}
	broker, tax, accounting, brokerRelations := deriveFees(skills, standings, station)
	if math.Abs(broker-.01) > 1e-9 || math.Abs(tax-.03375) > 1e-9 || accounting != 5 || brokerRelations != 5 {
		t.Fatalf("derived = %.5f/%.5f/%d/%d", broker, tax, accounting, brokerRelations)
	}
}

func TestDeriveFeesDefaultsForMissingSkills(t *testing.T) {
	broker, tax, _, _ := deriveFees(eveSkills{}, nil, eveStation{})
	if broker != .03 || tax != .075 {
		t.Fatalf("derived = %.3f/%.3f, want .030/.075", broker, tax)
	}
}

func TestResolveStationDefaultsToJita44(t *testing.T) {
	c := &eveClient{httpClient: &http.Client{Transport: roundTripper(func(r *http.Request) *http.Response {
		switch r.URL.Path {
		case "/latest/universe/stations/60003760/":
			return jsonResponse(http.StatusOK, `{"name":"Jita IV - Moon 4 - Caldari Navy Assembly Plant","owner":1000035,"race_id":1}`)
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
		return nil
	})}}
	old := eveAPIBase
	eveAPIBase = "https://esi.test/latest"
	defer func() { eveAPIBase = old }()
	station, err := c.resolveStation(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if station.ID != jita44ID || station.Name != jita44Name {
		t.Fatalf("station = %#v", station)
	}
}

func TestFactionForRace(t *testing.T) {
	for _, tc := range []struct{ race, faction int64 }{{1, 500001}, {2, 500002}, {4, 500003}, {8, 500004}, {99, 0}} {
		if got := factionForRace(tc.race); got != tc.faction {
			t.Errorf("factionForRace(%d) = %d, want %d", tc.race, got, tc.faction)
		}
	}
}

func TestResolveStationRejectsUnsupportedLocation(t *testing.T) {
	c := &eveClient{httpClient: &http.Client{Transport: roundTripper(func(r *http.Request) *http.Response { return jsonResponse(http.StatusOK, `{"name":"Structure"}`) })}}
	old := eveAPIBase
	eveAPIBase = "https://esi.test/latest"
	defer func() { eveAPIBase = old }()
	_, err := c.resolveStation(context.Background(), "60000001")
	if err == nil || !strings.Contains(err.Error(), "not a supported NPC station") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeUsesLocalCallbackAndPKCE(t *testing.T) {
	oldID, oldToken, oldAuthorize := eveClientID, eveSSOToken, eveSSOAuthorize
	defer func() { eveClientID, eveSSOToken, eveSSOAuthorize = oldID, oldToken, oldAuthorize }()
	eveClientID = "test-client"
	tokenServer := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") == "" || r.Form.Get("client_id") != "test-client" {
			t.Fatalf("form = %v", r.Form)
		}
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"CHARACTER:EVE:123","name":"Mikko"}`))
		fmt.Fprintf(w, `{"access_token":"x.%s.y","refresh_token":"refresh"}`, payload)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go tokenServer.Serve(ln)
	defer tokenServer.Close()
	eveSSOToken = "http://" + ln.Addr().String() + "/token"
	c := &eveClient{httpClient: http.DefaultClient, loginTimeout: time.Second, openBrowser: func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		if u.Query().Get("code_challenge_method") != "S256" {
			t.Fatal("missing PKCE method")
		}
		callback := eveCallbackURL + "?code=code&state=" + url.QueryEscape(u.Query().Get("state"))
		resp, err := http.Get(callback)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}}
	token, character, err := c.authorize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token.RefreshToken != "refresh" || character.ID != 123 || character.Name != "Mikko" {
		t.Fatalf("got %#v %#v", token, character)
	}
}

func TestCallbackRejectsMismatchedState(t *testing.T) {
	server, result, err := startCallbackServer("expected")
	if err != nil {
		t.Skipf("localhost callback port unavailable: %v", err)
	}
	defer server.Close()
	resp, err := http.Get(eveCallbackURL + "?code=x&state=wrong")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := (<-result).err; got == nil || !strings.Contains(got.Error(), "state") {
		t.Fatalf("result = %v", got)
	}
}

func TestCallbackReportsPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:8765")
	if err != nil {
		t.Skipf("localhost callback port unavailable: %v", err)
	}
	defer ln.Close()
	_, _, err = startCallbackServer("state")
	if err == nil {
		t.Fatal("startCallbackServer succeeded while port is occupied")
	}
}

func TestLoginTokenRefreshesSavedAuthorization(t *testing.T) {
	oldID, oldToken := eveClientID, eveSSOToken
	defer func() { eveClientID, eveSSOToken = oldID, oldToken }()
	eveClientID = "test-client"
	eveSSOToken = "https://sso.test/token"
	c := &eveClient{store: memoryCredentialStore{"iskrona/eve-refresh-token-42": "old"}, httpClient: &http.Client{Transport: roundTripper(func(r *http.Request) *http.Response {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old" || r.Form.Get("client_id") != "test-client" {
			t.Fatalf("form = %v", r.Form)
		}
		return jsonResponse(http.StatusOK, `{"access_token":"new-access","refresh_token":"new-refresh"}`)
	})}, openBrowser: func(string) error { t.Fatal("browser should not open for a saved authorization"); return nil }}
	token, character, err := c.loginToken(context.Background(), Config{EVE: EVEConfig{CharacterID: 42, CharacterName: "Mikko"}})
	if err != nil {
		t.Fatal(err)
	}
	if token.RefreshToken != "new-refresh" || character.ID != 42 {
		t.Fatalf("got %#v %#v", token, character)
	}
}

type roundTripper func(*http.Request) *http.Response

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d test", status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type memoryCredentialStore map[string]string

func (s memoryCredentialStore) Set(service, user, password string) error {
	s[service+"/"+user] = password
	return nil
}
func (s memoryCredentialStore) Get(service, user string) (string, error) {
	value, ok := s[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (s memoryCredentialStore) Delete(service, user string) error {
	delete(s, service+"/"+user)
	return nil
}
