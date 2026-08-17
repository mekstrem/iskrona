package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
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

func TestLoginPersistsDerivedFeesAuthorizationAndOutput(t *testing.T) {
	configPath := useIsolatedUserConfigDir(t)
	initial := DefaultConfig()
	initial.Market.MaxJumps = 7
	initial.EVE = EVEConfig{CharacterID: 42, CharacterName: "Old name"}
	if err := SaveConfig(configPath, initial); err != nil {
		t.Fatal(err)
	}

	oldAPI, oldToken := eveAPIBase, eveSSOToken
	defer func() { eveAPIBase, eveSSOToken = oldAPI, oldToken }()
	eveAPIBase, eveSSOToken = "https://esi.test/latest", "https://sso.test/token"
	store := memoryCredentialStore{"iskrona/eve-refresh-token-42": "old-refresh"}
	c := &eveClient{store: store, httpClient: &http.Client{Transport: roundTripper(func(r *http.Request) *http.Response {
		switch r.URL.String() {
		case "https://esi.test/latest/universe/stations/60003760/":
			return jsonResponse(http.StatusOK, `{"name":"Jita IV - Moon 4 - Caldari Navy Assembly Plant","owner":1000035,"race_id":1}`)
		case "https://sso.test/token":
			return jsonResponse(http.StatusOK, `{"access_token":"access","refresh_token":"new-refresh"}`)
		case "https://esi.test/latest/characters/42/skills/":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Fatalf("skills authorization = %q", r.Header.Get("Authorization"))
			}
			return jsonResponse(http.StatusOK, `{"skills":[{"skill_id":16622,"current_skill_level":5},{"skill_id":3446,"current_skill_level":5}]}`)
		case "https://esi.test/latest/characters/42/standings/":
			return jsonResponse(http.StatusOK, `[{"from_id":1000035,"from_type":"npc_corp","standing":10},{"from_id":500001,"from_type":"faction","standing":10}]`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
			return nil
		}
	})}, openBrowser: func(string) error { t.Fatal("browser should not open"); return nil }}

	var out strings.Builder
	if err := c.login(context.Background(), &out, ""); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.EVE.CharacterID != 42 || got.EVE.CharacterName != "Old name" || got.EVE.StationID != jita44ID || got.EVE.StationName != jita44Name {
		t.Fatalf("saved EVE config = %#v", got.EVE)
	}
	if math.Abs(got.Fees.Broker-.01) > 1e-9 || math.Abs(got.Fees.SalesTax-.03375) > 1e-9 {
		t.Fatalf("saved fees = %#v", got.Fees)
	}
	if got.Market.MaxJumps != 7 {
		t.Fatalf("unrelated config changed: %#v", got.Market)
	}
	if store["iskrona/eve-refresh-token-42"] != "new-refresh" {
		t.Fatalf("saved refresh token = %q", store["iskrona/eve-refresh-token-42"])
	}
	for _, want := range []string{"EVE fees updated", "Character: Old name", "Station: " + jita44Name, "Accounting: 5", "Broker Relations: 5", "Broker fee: 1.000%", "Sales tax: 3.375%"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestLoginDoesNotPersistConfigWhenCredentialSaveFails(t *testing.T) {
	configPath := useIsolatedUserConfigDir(t)
	initial := DefaultConfig()
	initial.EVE = EVEConfig{CharacterID: 42, CharacterName: "Mikko"}
	if err := SaveConfig(configPath, initial); err != nil {
		t.Fatal(err)
	}
	oldAPI, oldToken := eveAPIBase, eveSSOToken
	defer func() { eveAPIBase, eveSSOToken = oldAPI, oldToken }()
	eveAPIBase, eveSSOToken = "https://esi.test/latest", "https://sso.test/token"
	storeErr := errors.New("credential store unavailable")
	store := &recordingCredentialStore{values: map[string]string{"iskrona/eve-refresh-token-42": "old-refresh"}, setErr: storeErr}
	c := &eveClient{store: store, httpClient: &http.Client{Transport: roundTripper(func(r *http.Request) *http.Response {
		switch r.URL.Path {
		case "/latest/universe/stations/60003760/":
			return jsonResponse(http.StatusOK, `{"name":"Jita IV - Moon 4 - Caldari Navy Assembly Plant","owner":1000035,"race_id":1}`)
		case "/token":
			return jsonResponse(http.StatusOK, `{"access_token":"access","refresh_token":"new-refresh"}`)
		case "/latest/characters/42/skills/":
			return jsonResponse(http.StatusOK, `{"skills":[]}`)
		case "/latest/characters/42/standings/":
			return jsonResponse(http.StatusOK, `[]`)
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
			return nil
		}
	})}}
	if err := c.login(context.Background(), io.Discard, ""); err == nil || !strings.Contains(err.Error(), storeErr.Error()) {
		t.Fatalf("login error = %v", err)
	}
	got, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != initial {
		t.Fatalf("config changed after credential failure: got %#v, want %#v", got, initial)
	}
}

func TestLogoutDeletesCredentialAndClearsOnlyEVEMetadata(t *testing.T) {
	configPath := useIsolatedUserConfigDir(t)
	want := DefaultConfig()
	want.Market.MaxJumps = 9
	want.Fees = FeeConfig{Broker: .025, SalesTax: .035}
	want.EVE = EVEConfig{CharacterID: 42, CharacterName: "Mikko", StationID: jita44ID, StationName: jita44Name}
	if err := SaveConfig(configPath, want); err != nil {
		t.Fatal(err)
	}
	store := &recordingCredentialStore{values: map[string]string{"iskrona/eve-refresh-token-42": "refresh"}}
	c := &eveClient{store: store}
	var out strings.Builder
	if err := c.logout(&out); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "iskrona/eve-refresh-token-42" {
		t.Fatalf("deleted credentials = %#v", store.deleted)
	}
	got, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	want.EVE = EVEConfig{}
	if got != want {
		t.Fatalf("config after logout = %#v, want %#v", got, want)
	}
	if out.String() != "EVE authorization removed\n" {
		t.Fatalf("logout output = %q", out.String())
	}
}

func TestLogoutCredentialFailurePreservesConfig(t *testing.T) {
	configPath := useIsolatedUserConfigDir(t)
	want := DefaultConfig()
	want.EVE = EVEConfig{CharacterID: 42, CharacterName: "Mikko"}
	if err := SaveConfig(configPath, want); err != nil {
		t.Fatal(err)
	}
	storeErr := errors.New("delete denied")
	c := &eveClient{store: &recordingCredentialStore{deleteErr: storeErr}}
	if err := c.logout(io.Discard); err == nil || !strings.Contains(err.Error(), storeErr.Error()) {
		t.Fatalf("logout error = %v", err)
	}
	got, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("config changed after delete failure: got %#v, want %#v", got, want)
	}
}

func TestNewEVEClientUsesFiniteHTTPTimeout(t *testing.T) {
	c := newEVEClient()
	if c.httpClient.Timeout <= 0 {
		t.Fatalf("HTTP timeout = %s, want a finite timeout", c.httpClient.Timeout)
	}
}

func TestAuthorizeStopsWhenContextIsCanceled(t *testing.T) {
	oldAuthorize := eveSSOAuthorize
	defer func() { eveSSOAuthorize = oldAuthorize }()
	eveSSOAuthorize = "https://sso.test/authorize"
	ctx, cancel := context.WithCancel(context.Background())
	c := &eveClient{httpClient: &http.Client{}, loginTimeout: time.Minute, openBrowser: func(string) error {
		cancel()
		return nil
	}}
	started := time.Now()
	_, _, err := c.authorize(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("authorize error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("authorize took %s after cancellation", elapsed)
	}
}

func TestRefreshRejectsHTTPAndPayloadFailures(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		want string
	}{
		{"non-2xx", jsonResponse(http.StatusUnauthorized, `{}`), "refresh EVE authorization: 401 test"},
		{"malformed json", jsonResponse(http.StatusOK, `{`), "unexpected EOF"},
		{"missing refresh token", jsonResponse(http.StatusOK, `{"access_token":"access"}`), "response has no refresh token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &eveClient{httpClient: &http.Client{Transport: roundTripper(func(*http.Request) *http.Response { return tc.resp })}}
			_, err := c.refresh(context.Background(), "old")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refresh error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestRequestJSONSetsHeadersAndReportsFailures(t *testing.T) {
	c := &eveClient{httpClient: &http.Client{Transport: roundTripper(func(r *http.Request) *http.Response {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request = %s, headers = %v", r.Method, r.Header)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"name":"Jita"}` {
			t.Fatalf("body = %q", data)
		}
		return jsonResponse(http.StatusBadGateway, `{}`)
	})}}
	var destination map[string]any
	err := c.requestJSON(context.Background(), http.MethodPost, "https://esi.test/endpoint", []byte(`{"name":"Jita"}`), "token", &destination)
	if err == nil || !strings.Contains(err.Error(), "502 test") {
		t.Fatalf("requestJSON error = %v", err)
	}

	c.httpClient.Transport = roundTripper(func(*http.Request) *http.Response { return jsonResponse(http.StatusOK, `{`) })
	if err := c.requestJSON(context.Background(), http.MethodGet, "https://esi.test/endpoint", nil, "", &destination); err == nil {
		t.Fatal("requestJSON accepted malformed JSON")
	}
}

func TestCharacterFromJWTRejectsMalformedClaims(t *testing.T) {
	encode := func(payload string) string {
		return "x." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".y"
	}
	tests := []string{
		"not-a-jwt",
		"x.%%%.y",
		encode(`{`),
		encode(`{"sub":"CHARACTER:EVE"}`),
		encode(`{"sub":"CHARACTER:EVE:not-a-number"}`),
	}
	for _, token := range tests {
		if _, err := characterFromJWT(token); err == nil {
			t.Errorf("characterFromJWT(%q) succeeded, want error", token)
		}
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

type recordingCredentialStore struct {
	values    map[string]string
	setErr    error
	deleteErr error
	deleted   []string
}

func (s *recordingCredentialStore) Set(service, user, password string) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[service+"/"+user] = password
	return nil
}
func (s *recordingCredentialStore) Get(service, user string) (string, error) {
	value, ok := s.values[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (s *recordingCredentialStore) Delete(service, user string) error {
	key := service + "/" + user
	s.deleted = append(s.deleted, key)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.values, key)
	return nil
}

func useIsolatedUserConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "iskrona", "config.toml")
	if path != want {
		t.Fatalf("ConfigPath = %q, want %q", path, want)
	}
	return path
}
