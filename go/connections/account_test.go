package connections

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// fakeAccounts is an AccountSource backed by maps the test flips between
// requests. Keys are project + "/" + account. Token counts its calls per key,
// so a test can prove which account a connection's token was looked up under.
type fakeAccounts struct {
	mu       sync.Mutex
	statuses map[string]AccountStatus
	tokens   map[string]string
	errs     map[string]error
	calls    map[string]int
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{
		statuses: map[string]AccountStatus{},
		tokens:   map[string]string{},
		errs:     map[string]error{},
		calls:    map[string]int{},
	}
}

func (f *fakeAccounts) connect(project, account, email, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[project+"/"+account] = AccountStatus{Connected: true, Email: email}
	f.tokens[project+"/"+account] = token
	delete(f.errs, project+"/"+account)
}

func (f *fakeAccounts) disconnect(project, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.statuses, project+"/"+account)
	delete(f.tokens, project+"/"+account)
	f.errs[project+"/"+account] = ErrNotConnected
}

func (f *fakeAccounts) setStatus(project, account string, st AccountStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[project+"/"+account] = st
}

func (f *fakeAccounts) setTokenErr(project, account string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[project+"/"+account] = err
}

func (f *fakeAccounts) callCounts() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.calls))
	for k, v := range f.calls {
		out[k] = v
	}
	return out
}

func (f *fakeAccounts) Status(project, account string) AccountStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[project+"/"+account]
}

func (f *fakeAccounts) Token(_ context.Context, project, account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := project + "/" + account
	f.calls[key]++
	if err := f.errs[key]; err != nil {
		return "", err
	}
	tok, ok := f.tokens[key]
	if !ok {
		return "", ErrNotConnected
	}
	return tok, nil
}

var _ AccountSource = (*fakeAccounts)(nil)

// accountRegistry is project "enc" with three google_account connections —
// gmail and drive on the default account, sheets on "archive" — plus a
// bearer github whose env var is set, and project "wolf" with only bearer.
func accountRegistry(t *testing.T) *Registry {
	t.Helper()
	archive := validGoogleAccount()
	archive.URL = "https://sheetsmcp.googleapis.com/mcp/v1"
	archive.Auth.Account = "archive"
	drive := validGoogleAccount()
	drive.URL = "https://drivemcp.googleapis.com/mcp/v1"
	drive.Auth.Account = DefaultGoogleAccount // explicit, same as omitted
	reg, err := NewRegistry(map[string]map[string]Spec{
		"enc": {
			"gmail":  validGoogleAccount(),
			"drive":  drive,
			"sheets": archive,
			"github": validBearer(),
		},
		"wolf": {"github": validBearer()},
	}, func(k string) string {
		if k == "WOLF_GITHUB_PAT" {
			return "tok"
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

func infoByName(infos []Info, name string) (Info, bool) {
	for _, i := range infos {
		if i.Name == name {
			return i, true
		}
	}
	return Info{}, false
}

// With no AccountSource installed, every google_account connection is
// unavailable with the reason agentd gave, and bearer connections are
// untouched — in Availability, List and Servers alike.
func TestAccounts_NoSourceMeansDisabled(t *testing.T) {
	const reason = "Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set"
	reg := accountRegistry(t)
	reg.SetAccounts(nil, reason)

	for _, name := range []string{"gmail", "drive", "sheets"} {
		ok, got := reg.Availability("enc", name)
		if ok || got != reason {
			t.Errorf("Availability(%s) = (%v, %q), want (false, %q)", name, ok, got, reason)
		}
		info, _ := infoByName(reg.List("enc"), name)
		if info.Available || info.Unavailable != reason {
			t.Errorf("List %s = %+v, want unavailable with the disabled reason", name, info)
		}
	}
	if ok, got := reg.Availability("enc", "github"); !ok || got != "" {
		t.Errorf("bearer github Availability = (%v, %q), want (true, \"\")", ok, got)
	}
	if ok, _ := reg.Availability("wolf", "github"); !ok {
		t.Error("wolf's bearer github must stay available")
	}
	out := Servers(reg, "enc", []string{"*"}, "https://agentd.example.com")
	if len(out) != 1 {
		t.Fatalf("Servers with no source = %+v, want only github", out)
	}
	if _, ok := out["github"]; !ok {
		t.Fatalf("Servers with no source = %+v, want github", out)
	}
}

// A Registry whose SetAccounts was never called, or was called with an empty
// reason, still says something rather than an empty "unavailable".
func TestAccounts_NoSourceEmptyReasonStillExplains(t *testing.T) {
	for _, install := range []bool{false, true} {
		reg := accountRegistry(t)
		if install {
			reg.SetAccounts(nil, "")
		}
		ok, reason := reg.Availability("enc", "gmail")
		if ok || reason == "" {
			t.Errorf("install=%v: Availability = (%v, %q), want unavailable with a reason", install, ok, reason)
		}
	}
}

func TestAccounts_AvailabilityFollowsTheSource(t *testing.T) {
	reg := accountRegistry(t)
	src := newFakeAccounts()
	reg.SetAccounts(src, "")

	// Not connected: the connection says which account and what to press.
	ok, reason := reg.Availability("enc", "gmail")
	want := "gmail uses the Google account google, which is not connected — a project operator presses Connect Google in Settings"
	if ok || reason != want {
		t.Fatalf("not connected: Availability = (%v, %q), want (false, %q)", ok, reason, want)
	}
	if info, _ := infoByName(reg.List("enc"), "gmail"); info.Available || info.Unavailable != want {
		t.Fatalf("not connected: List gmail = %+v", info)
	}
	// enc's github is validBearer, whose WOLF_GITHUB_PAT is set: available.
	if out := Servers(reg, "enc", []string{"gmail", "github"}, "https://agentd.example.com"); len(out) != 1 || out["github"].URL == "" {
		t.Fatalf("not connected: Servers = %+v, want github only", out)
	}

	// Connected: available, Servers includes it; sheets (another account) is not.
	src.connect("enc", "google", "enc@example.com", "access-google")
	if ok, reason := reg.Availability("enc", "gmail"); !ok || reason != "" {
		t.Fatalf("connected: Availability = (%v, %q)", ok, reason)
	}
	if ok, _ := reg.Availability("enc", "drive"); !ok {
		t.Fatal("drive shares the google account and must be available too")
	}
	if ok, _ := reg.Availability("enc", "sheets"); ok {
		t.Fatal("sheets uses the archive account, which is not connected")
	}
	out := Servers(reg, "enc", []string{"*"}, "https://agentd.example.com")
	for _, name := range []string{"gmail", "drive", "github"} {
		if _, has := out[name]; !has {
			t.Errorf("connected: Servers missing %s: %+v", name, out)
		}
	}
	if _, has := out["sheets"]; has {
		t.Errorf("connected: Servers must omit sheets: %+v", out)
	}

	// Connected but the source cannot use it (the key changed): its reason.
	src.setStatus("enc", "google", AccountStatus{Connected: true, Email: "enc@example.com", Unavailable: KeyChangedReason("google")})
	ok, reason = reg.Availability("enc", "gmail")
	if ok || reason != "the stored Google connection for google can no longer be read (the encryption key changed) — connect Google again" {
		t.Fatalf("wrong key: Availability = (%v, %q)", ok, reason)
	}

	// Flipped back to disconnected: no rebuild needed.
	src.disconnect("enc", "google")
	if ok, _ := reg.Availability("enc", "gmail"); ok {
		t.Fatal("after disconnect gmail must be unavailable again")
	}
}

func TestAccounts_AvailabilityUnknownAndNil(t *testing.T) {
	reg := accountRegistry(t)
	if ok, reason := reg.Availability("enc", "nope"); ok || reason == "" {
		t.Errorf("unknown connection: (%v, %q), want unavailable with a reason", ok, reason)
	}
	var nilReg *Registry
	if ok, _ := nilReg.Availability("enc", "gmail"); ok {
		t.Error("nil registry must report unavailable")
	}
	if got := nilReg.Accounts("enc"); got != nil {
		t.Errorf("nil registry Accounts = %v, want nil", got)
	}
	nilReg.SetAccounts(newFakeAccounts(), "") // must not panic
}

// Static unavailability (an env var missing) still wins for bearer and
// google_oauth, exactly as before.
func TestAccounts_StaticUnavailableUnchanged(t *testing.T) {
	reg := testRegistry(t) // github available, gmail (google_oauth) missing env
	reg.SetAccounts(newFakeAccounts(), "")
	if ok, _ := reg.Availability("wolf", "github"); !ok {
		t.Error("bearer github must be available")
	}
	ok, reason := reg.Availability("wolf", "gmail")
	if ok || !strings.Contains(reason, "GOOGLE_CLIENT_ID") {
		t.Errorf("google_oauth gmail = (%v, %q), want the env-var reason", ok, reason)
	}
}

func TestAccounts_GroupsAndSorts(t *testing.T) {
	reg := accountRegistry(t)
	got := reg.Accounts("enc")
	if len(got) != 2 {
		t.Fatalf("Accounts = %+v, want two accounts", got)
	}
	if got[0].Account != "archive" || strings.Join(got[0].Connections, ",") != "sheets" {
		t.Errorf("first = %+v, want archive: [sheets]", got[0])
	}
	if got[1].Account != "google" || strings.Join(got[1].Connections, ",") != "drive,gmail" {
		t.Errorf("second = %+v, want google: [drive gmail]", got[1])
	}
	if got := reg.Accounts("wolf"); got != nil {
		t.Errorf("a project with only bearer connections has no accounts, got %+v", got)
	}
	if got := reg.Accounts("nope"); got != nil {
		t.Errorf("unknown project Accounts = %+v, want nil", got)
	}
}
