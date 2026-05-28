CREATE TABLE IF NOT EXISTS traffic_counter_state (
	node_id TEXT NOT NULL REFERENCES nodes(id),
	location_id TEXT NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
	rx_bytes INTEGER NOT NULL DEFAULT 0,
	tx_bytes INTEGER NOT NULL DEFAULT 0,
	last_sampled_at TEXT NOT NULL,
	reset_count INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(node_id, location_id)
);

CREATE INDEX IF NOT EXISTS idx_traffic_counters_client_location_time
ON traffic_counters(client_id, location_id, period_start);

CREATE INDEX IF NOT EXISTS idx_traffic_counters_location_time
ON traffic_counters(location_id, period_start);
