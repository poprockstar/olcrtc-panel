package metrics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"olcpanel/internal/supervisor"
)

const localNodeID = "local"

type StatusProvider interface {
	StatusSnapshot() map[string]supervisor.ProcessStatus
}

type HostReader interface {
	ReadHost(context.Context) (HostSnapshot, error)
}

type Options struct {
	StartedAt  time.Time
	Now        func() time.Time
	HostReader HostReader
	Statuses   StatusProvider
}

type Snapshot struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Panel       PanelSnapshot   `json:"panel"`
	Host        HostSnapshot    `json:"host"`
	Clients     ClientCounts    `json:"clients"`
	Locations   LocationCounts  `json:"locations"`
	Processes   ProcessCounts   `json:"processes"`
	Traffic     TrafficSnapshot `json:"traffic"`
	Quotas      QuotaCounts     `json:"quotas"`
	PerClient   []ClientSummary `json:"per_client"`
}

type PanelSnapshot struct {
	UptimeSeconds int64 `json:"uptime_seconds"`
}

type HostSnapshot struct {
	CPUPercent       *float64 `json:"cpu_percent"`
	MemoryTotalBytes *uint64  `json:"memory_total_bytes"`
	MemoryUsedBytes  *uint64  `json:"memory_used_bytes"`
	DiskTotalBytes   *uint64  `json:"disk_total_bytes"`
	DiskUsedBytes    *uint64  `json:"disk_used_bytes"`
}

type ClientCounts struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Disabled int `json:"disabled"`
	Expired  int `json:"expired"`
}

type LocationCounts struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Disabled int `json:"disabled"`
}

type ProcessCounts struct {
	Running int `json:"running"`
	Stopped int `json:"stopped"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
}

type TrafficSnapshot struct {
	TotalBytes int64 `json:"total_bytes"`
	RXBytes    int64 `json:"rx_bytes"`
	TXBytes    int64 `json:"tx_bytes"`
}

type QuotaCounts struct {
	Warning  int `json:"warning"`
	Exceeded int `json:"exceeded"`
}

type ClientSummary struct {
	ClientID      string        `json:"client_id"`
	Name          string        `json:"name"`
	TrafficBytes  int64         `json:"traffic_bytes"`
	RXBytes       int64         `json:"rx_bytes"`
	TXBytes       int64         `json:"tx_bytes"`
	QuotaBytes    *int64        `json:"quota_bytes"`
	QuotaWarning  bool          `json:"quota_warning"`
	QuotaExceeded bool          `json:"quota_exceeded"`
	Expired       bool          `json:"expired"`
	Locations     int           `json:"locations"`
	Processes     ProcessCounts `json:"processes"`
}

type clientRow struct {
	id             string
	name           string
	enabled        bool
	expiresAt      sql.NullString
	quotaBytes     sql.NullInt64
	quotaUsedBytes int64
}

type locationRow struct {
	id       string
	clientID string
	enabled  bool
}

type trafficRow struct {
	clientID string
	rx       int64
	tx       int64
}

func BuildSnapshot(ctx context.Context, db *sql.DB, options Options) (Snapshot, error) {
	if db == nil {
		return Snapshot{}, errors.New("metrics database is required")
	}
	now := time.Now().UTC
	if options.Now != nil {
		now = func() time.Time { return options.Now().UTC() }
	}
	generatedAt := now()
	clients, err := loadClients(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	locations, err := loadLocations(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	traffic, err := loadTraffic(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}

	var host HostSnapshot
	if options.HostReader != nil {
		host, err = options.HostReader.ReadHost(ctx)
		if err != nil {
			return Snapshot{}, err
		}
	} else {
		host, err = DefaultHostReader{}.ReadHost(ctx)
		if err != nil {
			return Snapshot{}, err
		}
	}

	statuses := map[string]supervisor.ProcessStatus{}
	if options.Statuses != nil {
		statuses = options.Statuses.StatusSnapshot()
	}

	snapshot := Snapshot{
		GeneratedAt: generatedAt,
		Panel: PanelSnapshot{
			UptimeSeconds: int64(generatedAt.Sub(options.StartedAt).Seconds()),
		},
		Host:      host,
		PerClient: make([]ClientSummary, 0, len(clients)),
	}
	if options.StartedAt.IsZero() {
		snapshot.Panel.UptimeSeconds = 0
	}

	summaries := make(map[string]*ClientSummary, len(clients))
	for _, client := range clients {
		snapshot.Clients.Total++
		if client.enabled {
			snapshot.Clients.Enabled++
		} else {
			snapshot.Clients.Disabled++
		}
		expired := isExpired(client.expiresAt, generatedAt)
		if expired {
			snapshot.Clients.Expired++
		}
		warning := quotaWarning(client.quotaBytes, client.quotaUsedBytes)
		exceeded := quotaExceeded(client.quotaBytes, client.quotaUsedBytes)
		if warning {
			snapshot.Quotas.Warning++
		}
		if exceeded {
			snapshot.Quotas.Exceeded++
		}
		var quota *int64
		if client.quotaBytes.Valid {
			value := client.quotaBytes.Int64
			quota = &value
		}
		summary := &ClientSummary{
			ClientID:      client.id,
			Name:          client.name,
			TrafficBytes:  client.quotaUsedBytes,
			QuotaBytes:    quota,
			QuotaWarning:  warning,
			QuotaExceeded: exceeded,
			Expired:       expired,
		}
		summaries[client.id] = summary
		snapshot.Traffic.TotalBytes += client.quotaUsedBytes
	}

	for _, row := range traffic {
		snapshot.Traffic.RXBytes += row.rx
		snapshot.Traffic.TXBytes += row.tx
		if summary := summaries[row.clientID]; summary != nil {
			summary.RXBytes = row.rx
			summary.TXBytes = row.tx
		}
	}

	for _, location := range locations {
		snapshot.Locations.Total++
		if location.enabled {
			snapshot.Locations.Enabled++
		} else {
			snapshot.Locations.Disabled++
		}
		status := normalizeStatus(statuses[location.id])
		addProcess(&snapshot.Processes, status)
		if summary := summaries[location.clientID]; summary != nil {
			summary.Locations++
			addProcess(&summary.Processes, status)
		}
	}

	for _, summary := range summaries {
		snapshot.PerClient = append(snapshot.PerClient, *summary)
	}
	sort.Slice(snapshot.PerClient, func(i, j int) bool {
		return snapshot.PerClient[i].ClientID < snapshot.PerClient[j].ClientID
	})
	return snapshot, nil
}

func loadClients(ctx context.Context, db *sql.DB) ([]clientRow, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, name, enabled, expires_at, quota_bytes, quota_used_bytes
FROM clients
WHERE node_id = ?
ORDER BY created_at, id`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("load metric clients: %w", err)
	}
	defer rows.Close()

	var result []clientRow
	for rows.Next() {
		var row clientRow
		var enabled int
		if err := rows.Scan(&row.id, &row.name, &enabled, &row.expiresAt, &row.quotaBytes, &row.quotaUsedBytes); err != nil {
			return nil, fmt.Errorf("scan metric client: %w", err)
		}
		row.enabled = enabled != 0
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric clients: %w", err)
	}
	return result, nil
}

func loadLocations(ctx context.Context, db *sql.DB) ([]locationRow, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, client_id, enabled
FROM locations
WHERE node_id = ?
ORDER BY created_at, id`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("load metric locations: %w", err)
	}
	defer rows.Close()

	var result []locationRow
	for rows.Next() {
		var row locationRow
		var enabled int
		if err := rows.Scan(&row.id, &row.clientID, &enabled); err != nil {
			return nil, fmt.Errorf("scan metric location: %w", err)
		}
		row.enabled = enabled != 0
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric locations: %w", err)
	}
	return result, nil
}

func loadTraffic(ctx context.Context, db *sql.DB) ([]trafficRow, error) {
	rows, err := db.QueryContext(ctx, `
SELECT client_id, COALESCE(SUM(rx_bytes), 0), COALESCE(SUM(tx_bytes), 0)
FROM traffic_counters
WHERE node_id = ?
GROUP BY client_id
ORDER BY client_id`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("load metric traffic: %w", err)
	}
	defer rows.Close()

	var result []trafficRow
	for rows.Next() {
		var row trafficRow
		if err := rows.Scan(&row.clientID, &row.rx, &row.tx); err != nil {
			return nil, fmt.Errorf("scan metric traffic: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric traffic: %w", err)
	}
	return result, nil
}

func normalizeStatus(status supervisor.ProcessStatus) supervisor.ProcessStatus {
	switch status {
	case supervisor.ProcessRunning, supervisor.ProcessStopped, supervisor.ProcessFailed:
		return status
	default:
		return supervisor.ProcessPending
	}
}

func addProcess(counts *ProcessCounts, status supervisor.ProcessStatus) {
	switch status {
	case supervisor.ProcessRunning:
		counts.Running++
	case supervisor.ProcessStopped:
		counts.Stopped++
	case supervisor.ProcessFailed:
		counts.Failed++
	default:
		counts.Pending++
	}
}

func isExpired(value sql.NullString, now time.Time) bool {
	if !value.Valid {
		return false
	}
	parsed, err := parseTime(value.String)
	return err == nil && !parsed.After(now)
}

func quotaWarning(quota sql.NullInt64, used int64) bool {
	return quota.Valid && quota.Int64 > 0 && used < quota.Int64 && float64(used)/float64(quota.Int64) >= 0.8
}

func quotaExceeded(quota sql.NullInt64, used int64) bool {
	return quota.Valid && used >= quota.Int64
}

func parseTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}
