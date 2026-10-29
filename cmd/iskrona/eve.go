package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

const (
	eveCallbackURL = "http://localhost:8765/callback"
	jita44Name     = "Jita IV - Moon 4 - Caldari Navy Assembly Plant"
	jita44ID       = int64(60003760)
	accountingID   = int64(16622)
	brokerSkillID  = int64(3446)
)

// eveClientID identifies ISKrona's public EVE SSO application. Client IDs are
// not secrets; the application must be registered with eveCallbackURL.
var eveClientID = "0161b27cea544e16bae13c3e8660b61a"

var eveAPIBase = "https://esi.evetech.net/latest"
var eveSSOAuthorize = "https://login.eveonline.com/v2/oauth/authorize"
var eveSSOToken = "https://login.eveonline.com/v2/oauth/token"

type credentialStore interface {
	Set(string, string, string) error
	Get(string, string) (string, error)
	Delete(string, string) error
}
type systemCredentialStore struct{}

func (systemCredentialStore) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (systemCredentialStore) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (systemCredentialStore) Delete(s, u string) error        { return keyring.Delete(s, u) }

type eveClient struct {
	httpClient   *http.Client
	store        credentialStore
	openBrowser  func(string) error
	loginTimeout time.Duration
}

func newEVEClient() *eveClient {
	return &eveClient{httpClient: &http.Client{Timeout: 30 * time.Second}, store: systemCredentialStore{}, openBrowser: openBrowser, loginTimeout: 2 * time.Minute}
}

func newLoginCmd() *cobra.Command {
	var station string
	cmd := &cobra.Command{Use: "login", Short: "Log in to EVE and populate trading fees", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return newEVEClient().login(cmd.Context(), cmd.OutOrStdout(), station)
	}}
	cmd.Flags().StringVar(&station, "station", jita44Name, "NPC station name or ID")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{Use: "logout", Short: "Remove saved EVE authorization", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return newEVEClient().logout(cmd.OutOrStdout()) }}
}

func (c *eveClient) login(ctx context.Context, out io.Writer, stationInput string) error {
	if eveClientID == "" {
		return commandError(errors.New("EVE SSO client ID is not configured in this build"))
	}
	station, err := c.resolveStation(ctx, stationInput)
	if err != nil {
		return commandError(err)
	}
	path, err := ConfigPath()
	if err != nil {
		return commandError(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		return commandError(err)
	}
	token, character, err := c.loginToken(ctx, cfg)
	if err != nil {
		return commandError(err)
	}
	skills, err := c.skills(ctx, character.ID, token.AccessToken)
	if err != nil {
		return commandError(err)
	}
	standings, err := c.standings(ctx, character.ID, token.AccessToken)
	if err != nil {
		return commandError(err)
	}
	broker, tax, accounting, brokerRelations := deriveFees(skills, standings, station)
	cfg.Fees.Broker, cfg.Fees.SalesTax = broker, tax
	cfg.EVE = EVEConfig{CharacterID: character.ID, CharacterName: character.Name, StationID: station.ID, StationName: station.Name}
	if err := c.store.Set("iskrona", credentialKey(character.ID), token.RefreshToken); err != nil {
		return commandError(fmt.Errorf("save EVE authorization in credential store: %w", err))
	}
	if err := SaveConfig(path, cfg); err != nil {
		return commandError(err)
	}
	fmt.Fprintf(out, "EVE fees updated\nCharacter: %s\nStation: %s\nAccounting: %d\nBroker Relations: %d\nBroker fee: %.3f%%\nSales tax: %.3f%%\n", character.Name, station.Name, accounting, brokerRelations, broker*100, tax*100)
	return nil
}

// loginToken reuses a saved authorization when possible. A revoked or missing
// credential deliberately falls back to the browser so `iskrona login` is the
// single recovery path for an expired EVE authorization.
func (c *eveClient) loginToken(ctx context.Context, cfg Config) (eveToken, eveCharacter, error) {
	if cfg.EVE.CharacterID != 0 {
		if refresh, err := c.store.Get("iskrona", credentialKey(cfg.EVE.CharacterID)); err == nil {
			if token, err := c.refresh(ctx, refresh); err == nil {
				return token, eveCharacter{ID: cfg.EVE.CharacterID, Name: cfg.EVE.CharacterName}, nil
			}
		}
	}
	return c.authorize(ctx)
}

func (c *eveClient) logout(out io.Writer) error {
	path, err := ConfigPath()
	if err != nil {
		return commandError(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		return commandError(err)
	}
	if cfg.EVE.CharacterID != 0 {
		if err := c.store.Delete("iskrona", credentialKey(cfg.EVE.CharacterID)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return commandError(fmt.Errorf("remove EVE authorization: %w", err))
		}
	}
	cfg.EVE = EVEConfig{}
	if err := SaveConfig(path, cfg); err != nil {
		return commandError(err)
	}
	fmt.Fprintln(out, "EVE authorization removed")
	return nil
}

type eveStation struct {
	ID            int64
	Name          string
	OwnerID       int64 `json:"owner"`
	RaceID        int64 `json:"race_id"`
	CorporationID int64 `json:"corporation_id"`
	FactionID     int64 `json:"faction_id"`
}
type eveCharacter struct {
	ID   int64
	Name string
}
type eveToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}
type eveSkill struct {
	SkillID      int64 `json:"skill_id"`
	CurrentLevel int   `json:"current_skill_level"`
	ActiveLevel  int   `json:"active_skill_level"`
}
type eveSkills struct {
	Skills []eveSkill `json:"skills"`
}
type eveStanding struct {
	FromID   int64   `json:"from_id"`
	FromType string  `json:"from_type"`
	Standing float64 `json:"standing"`
}

func deriveFees(skills eveSkills, standings []eveStanding, station eveStation) (broker, salesTax float64, accounting, brokerRelations int) {
	for _, skill := range skills.Skills {
		level := skill.CurrentLevel
		if skill.ActiveLevel > level {
			level = skill.ActiveLevel
		}
		if skill.SkillID == accountingID {
			accounting = level
		}
		if skill.SkillID == brokerSkillID {
			brokerRelations = level
		}
	}
	var faction, corporation float64
	for _, s := range standings {
		if s.FromID == station.FactionID && s.FromType == "faction" {
			faction = s.Standing
		}
		if s.FromID == station.CorporationID && (s.FromType == "npc_corp" || s.FromType == "corporation") {
			corporation = s.Standing
		}
	}
	broker = .03 - .003*float64(brokerRelations) - .0003*faction - .0002*corporation
	if broker < .01 {
		broker = .01
	}
	salesTax = .075 * (1 - .11*float64(accounting))
	return
}

func (c *eveClient) resolveStation(ctx context.Context, value string) (eveStation, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = jita44Name
	}
	var id int64
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		id = n
	} else if strings.EqualFold(value, jita44Name) || strings.EqualFold(value, "jita 4-4") {
		id = jita44ID
	} else {
		body, _ := json.Marshal([]string{value})
		var found struct {
			Stations []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"stations"`
		}
		if err := c.requestJSON(ctx, http.MethodPost, eveAPIBase+"/universe/ids/", body, "", &found); err != nil {
			return eveStation{}, fmt.Errorf("resolve station %q: %w", value, err)
		}
		if len(found.Stations) != 1 {
			return eveStation{}, fmt.Errorf("station %q is not an unambiguous NPC station", value)
		}
		id = found.Stations[0].ID
	}
	var station eveStation
	if err := c.requestJSON(ctx, http.MethodGet, fmt.Sprintf("%s/universe/stations/%d/", eveAPIBase, id), nil, "", &station); err != nil {
		return eveStation{}, fmt.Errorf("load station: %w", err)
	}
	station.ID = id
	// ESI station responses identify their owner as `owner`. They expose the
	// station race rather than an NPC-faction ID, so map the four empire races
	// to their corresponding faction (Jita's race is Caldari).
	if station.CorporationID == 0 {
		station.CorporationID = station.OwnerID
	}
	if station.CorporationID == 0 {
		return eveStation{}, errors.New("selected location is not a supported NPC station; set fees manually for player structures")
	}
	if station.FactionID == 0 {
		station.FactionID = factionForRace(station.RaceID)
	}
	if station.FactionID == 0 {
		return eveStation{}, errors.New("selected station owner has no NPC faction; set fees manually for player structures")
	}
	return station, nil
}

func factionForRace(raceID int64) int64 {
	switch raceID {
	case 1: // Caldari
		return 500001
	case 2: // Minmatar
		return 500002
	case 4: // Amarr
		return 500003
	case 8: // Gallente
		return 500004
	default:
		return 0
	}
}

func (c *eveClient) authorize(ctx context.Context) (eveToken, eveCharacter, error) {
	state, err := randomURLValue(24)
	if err != nil {
		return eveToken{}, eveCharacter{}, err
	}
	verifier, err := randomURLValue(48)
	if err != nil {
		return eveToken{}, eveCharacter{}, err
	}
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	server, callback, err := startCallbackServer(state)
	if err != nil {
		return eveToken{}, eveCharacter{}, fmt.Errorf("start local callback at %s: %w", eveCallbackURL, err)
	}
	defer server.Close()
	u, _ := url.Parse(eveSSOAuthorize)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("redirect_uri", eveCallbackURL)
	q.Set("client_id", eveClientID)
	q.Set("scope", "esi-skills.read_skills.v1 esi-characters.read_standings.v1")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	if err := c.openBrowser(u.String()); err != nil {
		return eveToken{}, eveCharacter{}, fmt.Errorf("open EVE login in browser: %w", err)
	}
	loginTimer := time.NewTimer(c.loginTimeout)
	defer loginTimer.Stop()
	select {
	case result := <-callback:
		if result.err != nil {
			return eveToken{}, eveCharacter{}, result.err
		}
		form := url.Values{"grant_type": {"authorization_code"}, "code": {result.code}, "code_verifier": {verifier}, "client_id": {eveClientID}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, eveSSOToken, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return eveToken{}, eveCharacter{}, fmt.Errorf("exchange EVE authorization: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return eveToken{}, eveCharacter{}, fmt.Errorf("exchange EVE authorization: %s", resp.Status)
		}
		var token eveToken
		if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
			return eveToken{}, eveCharacter{}, err
		}
		character, err := characterFromJWT(token.AccessToken)
		return token, character, err
	case <-loginTimer.C:
		return eveToken{}, eveCharacter{}, errors.New("EVE login timed out")
	case <-ctx.Done():
		return eveToken{}, eveCharacter{}, ctx.Err()
	}
}

func (c *eveClient) refresh(ctx context.Context, refreshToken string) (eveToken, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {eveClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, eveSSOToken, strings.NewReader(form.Encode()))
	if err != nil {
		return eveToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return eveToken{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return eveToken{}, fmt.Errorf("refresh EVE authorization: %s", resp.Status)
	}
	var token eveToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return eveToken{}, err
	}
	if token.RefreshToken == "" {
		return eveToken{}, errors.New("refresh EVE authorization: response has no refresh token")
	}
	return token, nil
}

type callbackResult struct {
	code string
	err  error
}

func startCallbackServer(expectedState string) (*http.Server, <-chan callbackResult, error) {
	result := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	server := &http.Server{Addr: "localhost:8765", Handler: mux}
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != expectedState {
			http.Error(w, "Invalid login state", http.StatusBadRequest)
			result <- callbackResult{err: errors.New("invalid EVE login state")}
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			http.Error(w, "EVE login failed", http.StatusBadRequest)
			result <- callbackResult{err: fmt.Errorf("EVE login failed: %s", e)}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			result <- callbackResult{err: errors.New("EVE login callback has no authorization code")}
			return
		}
		fmt.Fprintln(w, "EVE login complete. You can close this page.")
		result <- callbackResult{code: code}
	})
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, nil, err
	}
	go server.Serve(ln)
	return server, result, nil
}

func (c *eveClient) skills(ctx context.Context, id int64, token string) (eveSkills, error) {
	var out eveSkills
	return out, c.requestJSON(ctx, http.MethodGet, fmt.Sprintf("%s/characters/%d/skills/", eveAPIBase, id), nil, token, &out)
}
func (c *eveClient) standings(ctx context.Context, id int64, token string) ([]eveStanding, error) {
	var out []eveStanding
	return out, c.requestJSON(ctx, http.MethodGet, fmt.Sprintf("%s/characters/%d/standings/", eveAPIBase, id), nil, token, &out)
}
func (c *eveClient) requestJSON(ctx context.Context, method, address string, body []byte, token string, destination any) error {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(destination)
}
func credentialKey(characterID int64) string { return fmt.Sprintf("eve-refresh-token-%d", characterID) }
func randomURLValue(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func characterFromJWT(token string) (eveCharacter, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return eveCharacter{}, errors.New("invalid EVE access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return eveCharacter{}, err
	}
	var claims struct {
		Sub  string `json:"sub"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return eveCharacter{}, err
	}
	fields := strings.Split(claims.Sub, ":")
	if len(fields) != 3 {
		return eveCharacter{}, errors.New("invalid EVE character identity")
	}
	id, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return eveCharacter{}, err
	}
	return eveCharacter{ID: id, Name: claims.Name}, nil
}
func openBrowser(address string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", address).Start()
	case "darwin":
		return exec.Command("open", address).Start()
	default:
		return exec.Command("xdg-open", address).Start()
	}
}
