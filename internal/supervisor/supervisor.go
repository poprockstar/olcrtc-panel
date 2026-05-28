package supervisor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const localNodeID = "local"

type Action string

const (
	ActionStarted   Action = "started"
	ActionRestarted Action = "restarted"
	ActionStopped   Action = "stopped"
	ActionUnchanged Action = "unchanged"
	ActionSkipped   Action = "skipped"
)

type Reason string

const (
	ReasonNew              Reason = "new"
	ReasonChanged          Reason = "changed"
	ReasonRemoved          Reason = "removed"
	ReasonDisabledClient   Reason = "disabled_client"
	ReasonDisabledLocation Reason = "disabled_location"
	ReasonExpiredClient    Reason = "expired_client"
	ReasonQuotaLocked      Reason = "quota_locked"
	ReasonUnchanged        Reason = "unchanged"
)

type Summary struct {
	Started   int `json:"started"`
	Restarted int `json:"restarted"`
	Stopped   int `json:"stopped"`
	Unchanged int `json:"unchanged"`
	Skipped   int `json:"skipped"`
}

type ActionResult struct {
	LocationID string `json:"location_id"`
	ClientID   string `json:"client_id"`
	Action     Action `json:"action"`
	Reason     Reason `json:"reason"`
}

type ReloadResult struct {
	Summary Summary        `json:"summary"`
	Actions []ActionResult `json:"actions"`
}

type LocationState struct {
	LocationID       string
	ClientID         string
	Name             string
	Provider         string
	Transport        string
	RoomID           string
	CryptoKey        string
	TransportPayload string
	DNS              string
	fingerprint      string
}

type Runner interface {
	Start(context.Context, LocationState) error
	Restart(context.Context, LocationState, LocationState) error
	Stop(context.Context, LocationState) error
	Status(locationID string) ProcessStatus
}

type NoopRunner struct{}

func (NoopRunner) Start(context.Context, LocationState) error {
	return nil
}

func (NoopRunner) Restart(context.Context, LocationState, LocationState) error {
	return nil
}

func (NoopRunner) Stop(context.Context, LocationState) error {
	return nil
}

func (NoopRunner) Status(string) ProcessStatus {
	return ProcessRunning
}

type Supervisor struct {
	db      *sql.DB
	runner  Runner
	now     func() time.Time
	mu      sync.Mutex
	running map[string]LocationState
}

type Option func(*Supervisor)

func WithRunner(runner Runner) Option {
	return func(supervisor *Supervisor) {
		if runner != nil {
			supervisor.runner = runner
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(supervisor *Supervisor) {
		if now != nil {
			supervisor.now = now
		}
	}
}

func New(db *sql.DB, options ...Option) *Supervisor {
	supervisor := &Supervisor{
		db:      db,
		runner:  NoopRunner{},
		now:     func() time.Time { return time.Now().UTC() },
		running: make(map[string]LocationState),
	}
	for _, option := range options {
		option(supervisor)
	}
	return supervisor
}

func (supervisor *Supervisor) Reload(ctx context.Context) (ReloadResult, error) {
	if supervisor == nil || supervisor.db == nil {
		return ReloadResult{}, errors.New("supervisor database is required")
	}
	entries, err := loadDesired(ctx, supervisor.db, supervisor.now())
	if err != nil {
		return ReloadResult{}, err
	}

	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()

	result := ReloadResult{Actions: make([]ActionResult, 0, len(entries)+len(supervisor.running))}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		seen[entry.state.LocationID] = struct{}{}
		current, isRunning := supervisor.running[entry.state.LocationID]
		switch {
		case entry.reason != "":
			if isRunning {
				if err := supervisor.runner.Stop(ctx, current); err != nil {
					return ReloadResult{}, fmt.Errorf("stop location %s: %w", current.LocationID, err)
				}
				delete(supervisor.running, current.LocationID)
				result.add(ActionResult{LocationID: entry.state.LocationID, ClientID: entry.state.ClientID, Action: ActionStopped, Reason: entry.reason})
			} else {
				result.add(ActionResult{LocationID: entry.state.LocationID, ClientID: entry.state.ClientID, Action: ActionSkipped, Reason: entry.reason})
			}
		case isRunning && !supervisor.runnerRunning(entry.state.LocationID):
			delete(supervisor.running, entry.state.LocationID)
			if err := supervisor.runner.Start(ctx, entry.state); err != nil {
				return ReloadResult{}, fmt.Errorf("start location %s: %w", entry.state.LocationID, err)
			}
			supervisor.running[entry.state.LocationID] = entry.state
			result.add(ActionResult{LocationID: entry.state.LocationID, ClientID: entry.state.ClientID, Action: ActionStarted, Reason: ReasonNew})
		case !isRunning:
			if err := supervisor.runner.Start(ctx, entry.state); err != nil {
				return ReloadResult{}, fmt.Errorf("start location %s: %w", entry.state.LocationID, err)
			}
			supervisor.running[entry.state.LocationID] = entry.state
			result.add(ActionResult{LocationID: entry.state.LocationID, ClientID: entry.state.ClientID, Action: ActionStarted, Reason: ReasonNew})
		case current.fingerprint != entry.state.fingerprint:
			if err := supervisor.runner.Restart(ctx, current, entry.state); err != nil {
				return ReloadResult{}, fmt.Errorf("restart location %s: %w", entry.state.LocationID, err)
			}
			supervisor.running[entry.state.LocationID] = entry.state
			result.add(ActionResult{LocationID: entry.state.LocationID, ClientID: entry.state.ClientID, Action: ActionRestarted, Reason: ReasonChanged})
		default:
			result.add(ActionResult{LocationID: entry.state.LocationID, ClientID: entry.state.ClientID, Action: ActionUnchanged, Reason: ReasonUnchanged})
		}
	}

	removed := make([]LocationState, 0)
	for id, state := range supervisor.running {
		if _, ok := seen[id]; !ok {
			removed = append(removed, state)
		}
	}
	sort.Slice(removed, func(i, j int) bool {
		return removed[i].LocationID < removed[j].LocationID
	})
	for _, state := range removed {
		if err := supervisor.runner.Stop(ctx, state); err != nil {
			return ReloadResult{}, fmt.Errorf("stop removed location %s: %w", state.LocationID, err)
		}
		delete(supervisor.running, state.LocationID)
		result.add(ActionResult{LocationID: state.LocationID, ClientID: state.ClientID, Action: ActionStopped, Reason: ReasonRemoved})
	}

	return result, nil
}

func (supervisor *Supervisor) runnerRunning(locationID string) bool {
	return supervisor.runner.Status(locationID) == ProcessRunning
}

func (result *ReloadResult) add(action ActionResult) {
	result.Actions = append(result.Actions, action)
	switch action.Action {
	case ActionStarted:
		result.Summary.Started++
	case ActionRestarted:
		result.Summary.Restarted++
	case ActionStopped:
		result.Summary.Stopped++
	case ActionUnchanged:
		result.Summary.Unchanged++
	case ActionSkipped:
		result.Summary.Skipped++
	}
}

type desiredEntry struct {
	state  LocationState
	reason Reason
}

func loadDesired(ctx context.Context, db *sql.DB, now time.Time) ([]desiredEntry, error) {
	quotaLockMode, err := quotaLockMode(ctx, db)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
SELECT
	l.id, l.client_id, l.name, l.enabled, l.provider, l.transport, l.room_id, l.crypto_key, l.transport_payload, l.dns,
	c.enabled, c.expires_at, c.quota_bytes, c.quota_used_bytes
FROM locations l
JOIN clients c ON c.id = l.client_id AND c.node_id = l.node_id
WHERE l.node_id = ?
ORDER BY l.created_at, l.id`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("load supervisor desired state: %w", err)
	}
	defer rows.Close()

	var entries []desiredEntry
	for rows.Next() {
		var entry desiredEntry
		var locationEnabled, clientEnabled int
		var expires sql.NullString
		var quotaBytes sql.NullInt64
		var quotaUsed int64
		if err := rows.Scan(
			&entry.state.LocationID,
			&entry.state.ClientID,
			&entry.state.Name,
			&locationEnabled,
			&entry.state.Provider,
			&entry.state.Transport,
			&entry.state.RoomID,
			&entry.state.CryptoKey,
			&entry.state.TransportPayload,
			&entry.state.DNS,
			&clientEnabled,
			&expires,
			&quotaBytes,
			&quotaUsed,
		); err != nil {
			return nil, fmt.Errorf("scan supervisor desired state: %w", err)
		}

		switch {
		case clientEnabled == 0:
			entry.reason = ReasonDisabledClient
		case locationEnabled == 0:
			entry.reason = ReasonDisabledLocation
		case expired(expires, now):
			entry.reason = ReasonExpiredClient
		case quotaLockMode == "stop" && quotaBytes.Valid && quotaUsed >= quotaBytes.Int64:
			entry.reason = ReasonQuotaLocked
		}
		entry.state.fingerprint = fingerprint(entry.state)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate supervisor desired state: %w", err)
	}
	return entries, nil
}

func quotaLockMode(ctx context.Context, db *sql.DB) (string, error) {
	var value string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'quota_lock_mode'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "stop", nil
	}
	if err != nil {
		return "", fmt.Errorf("read quota lock mode: %w", err)
	}
	return value, nil
}

func expired(value sql.NullString, now time.Time) bool {
	if !value.Valid {
		return false
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return false
	}
	return !parsed.After(now)
}

func parseTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func fingerprint(state LocationState) string {
	data, _ := json.Marshal(struct {
		LocationID       string `json:"location_id"`
		ClientID         string `json:"client_id"`
		Name             string `json:"name"`
		Provider         string `json:"provider"`
		Transport        string `json:"transport"`
		RoomID           string `json:"room_id"`
		CryptoKey        string `json:"crypto_key"`
		TransportPayload string `json:"transport_payload"`
		DNS              string `json:"dns"`
	}{
		LocationID:       state.LocationID,
		ClientID:         state.ClientID,
		Name:             state.Name,
		Provider:         state.Provider,
		Transport:        state.Transport,
		RoomID:           state.RoomID,
		CryptoKey:        state.CryptoKey,
		TransportPayload: state.TransportPayload,
		DNS:              state.DNS,
	})
	return string(data)
}
