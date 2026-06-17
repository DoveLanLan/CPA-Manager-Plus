package automation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type stubSettingsStore struct {
	settings store.AutomationSettings
	ok       bool
	err      error
	saves    int
}

func (s *stubSettingsStore) LoadAutomationSettings(ctx context.Context) (store.AutomationSettings, bool, error) {
	return s.settings, s.ok, s.err
}

func (s *stubSettingsStore) SaveAutomationSettings(ctx context.Context, settings store.AutomationSettings) error {
	s.saves++
	s.settings = settings
	s.ok = true
	return nil
}

func mustStatus(t *testing.T, svc *Service, ctx context.Context) Status {
	t.Helper()
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("unexpected Status error: %v", err)
	}
	return status
}

func TestStatusExposesEffectiveFlagsAndKeys(t *testing.T) {
	cfg := config.Config{
		QuotaCooldownEnabled:      true,
		AccountActionsEnabled:     true,
		AccountActionsAutoDisable: false,
	}
	status := mustStatus(t, New(cfg), context.Background())

	if status.Source != SourceStartup {
		t.Fatalf("source = %q, want %q", status.Source, SourceStartup)
	}

	if !status.QuotaCooldown.Enabled || status.QuotaCooldown.EnvKey != "USAGE_QUOTA_COOLDOWN_ENABLED" || status.QuotaCooldown.ConfigFileKey != "quotaCooldownEnabled" {
		t.Fatalf("quotaCooldown = %#v", status.QuotaCooldown)
	}
	if status.QuotaCooldown.DependsOn != "" {
		t.Fatalf("quotaCooldown should not declare a dependency, got %q", status.QuotaCooldown.DependsOn)
	}

	if !status.AccountActions.Enabled || status.AccountActions.EnvKey != "USAGE_ACCOUNT_ACTIONS_ENABLED" || status.AccountActions.ConfigFileKey != "accountActionsEnabled" {
		t.Fatalf("accountActions = %#v", status.AccountActions)
	}
	if status.AccountActions.DependsOn != "" {
		t.Fatalf("accountActions should not declare a dependency, got %q", status.AccountActions.DependsOn)
	}

	if status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("accountActionsAutoDisable should be disabled, got %#v", status.AccountActionsAutoDisable)
	}
	if status.AccountActionsAutoDisable.EnvKey != "USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE" || status.AccountActionsAutoDisable.ConfigFileKey != "accountActionsAutoDisable" {
		t.Fatalf("accountActionsAutoDisable keys = %#v", status.AccountActionsAutoDisable)
	}
	if status.AccountActionsAutoDisable.DependsOn != "authIssueQueue" {
		t.Fatalf("accountActionsAutoDisable dependsOn = %q", status.AccountActionsAutoDisable.DependsOn)
	}
}

func TestStatusAutoDisableReportsEffectiveValue(t *testing.T) {
	status := mustStatus(t, New(config.Config{
		AccountActionsEnabled:     false,
		AccountActionsAutoDisable: true,
	}), context.Background())
	if status.AccountActions.Enabled {
		t.Fatalf("accountActions should be disabled, got %#v", status.AccountActions)
	}
	if status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("accountActionsAutoDisable should not be effective when accountActions is disabled, got %#v", status.AccountActionsAutoDisable)
	}
	if status.AccountActionsAutoDisable.DependsOn != "authIssueQueue" {
		t.Fatalf("accountActionsAutoDisable dependsOn = %q", status.AccountActionsAutoDisable.DependsOn)
	}

	status = mustStatus(t, New(config.Config{
		AccountActionsEnabled:     true,
		AccountActionsAutoDisable: true,
	}), context.Background())
	if !status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("accountActionsAutoDisable should be effective when accountActions is enabled, got %#v", status.AccountActionsAutoDisable)
	}
}

func TestStatusUsesDBSettingsUnlessEnvLocked(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/usage.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.SaveAutomationSettings(ctx, store.AutomationSettings{
		QuotaCooldownEnabled:      boolPtr(true),
		AccountActionsEnabled:     boolPtr(false),
		AccountActionsAutoDisable: boolPtr(true),
	}); err != nil {
		t.Fatalf("save automation settings: %v", err)
	}
	status := mustStatus(t, New(config.Config{}, st), ctx)
	if !status.QuotaCooldown.Enabled || status.QuotaCooldown.Source != SourceDB || status.QuotaCooldown.Locked {
		t.Fatalf("quotaCooldown = %#v", status.QuotaCooldown)
	}
	if status.AccountActions.Enabled || status.AccountActions.Source != SourceDB {
		t.Fatalf("accountActions = %#v", status.AccountActions)
	}
	if status.AccountActionsAutoDisable.Enabled || !status.AccountActionsAutoDisable.Configured || status.AccountActionsAutoDisable.Source != SourceDB {
		t.Fatalf("accountActionsAutoDisable = %#v", status.AccountActionsAutoDisable)
	}

	status = mustStatus(t, New(config.Config{QuotaCooldownEnabled: false, QuotaCooldownEnvSet: true}, st), ctx)
	if status.QuotaCooldown.Enabled || !status.QuotaCooldown.Locked || status.QuotaCooldown.Source != SourceEnv {
		t.Fatalf("env locked quotaCooldown = %#v", status.QuotaCooldown)
	}
}

func TestUpdateRejectsEnvLockedFields(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/usage.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	_, err = New(config.Config{AccountActionsEnvSet: true}, st).Update(context.Background(), UpdateRequest{AccountActionsEnabled: boolPtr(true)})
	if err == nil || !strings.Contains(err.Error(), "locked by environment variable") {
		t.Fatalf("Update err = %v", err)
	}
}

func TestStatusDefaultsAllOff(t *testing.T) {
	status := mustStatus(t, New(config.Config{}), context.Background())
	if status.QuotaCooldown.Enabled || status.AccountActions.Enabled || status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("expected all capabilities disabled by default, got %#v", status)
	}
}

func TestStatusReturnsErrorWhenLoadFails(t *testing.T) {
	svc := &Service{
		cfg:   config.Config{QuotaCooldownEnabled: true},
		store: &stubSettingsStore{err: errors.New("simulated db read failure")},
	}
	if _, err := svc.Status(context.Background()); err == nil {
		t.Fatalf("expected Status to surface load error, got nil")
	}
}

func TestRuntimeSettingsKeepsLastKnownOnLoadError(t *testing.T) {
	ctx := context.Background()
	loader := &stubSettingsStore{
		settings: store.AutomationSettings{QuotaCooldownEnabled: boolPtr(true)},
		ok:       true,
	}
	svc := &Service{cfg: config.Config{}, store: loader}

	first := svc.RuntimeSettings(ctx)
	if !first.QuotaCooldownEnabled {
		t.Fatalf("first runtime load should reflect DB value, got %#v", first)
	}

	// Simulate a later read failure (e.g. SQLite error / corrupted JSON).
	loader.err = errors.New("simulated db read failure")
	second := svc.RuntimeSettings(ctx)
	if !second.QuotaCooldownEnabled {
		t.Fatalf("runtime gating should keep last known config on read failure, got %#v", second)
	}

	// A service that has never loaded successfully falls back to startup defaults.
	fresh := &Service{
		cfg:   config.Config{QuotaCooldownEnabled: false},
		store: &stubSettingsStore{err: errors.New("simulated db read failure")},
	}
	fallback := fresh.RuntimeSettings(ctx)
	if fallback.QuotaCooldownEnabled {
		t.Fatalf("runtime gating should fall back to startup default before first successful load, got %#v", fallback)
	}
}
