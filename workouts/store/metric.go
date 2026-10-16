package store

import (
	"context"
	"time"
)

// Metrics is the closed set of body-measurement series, in display
// order, with their unit kind (weight|percent|energy|length).
var Metrics = []struct{ Key, Label, Unit string }{
	{"weight_kg", "Weight", "weight"},
	{"body_fat_pct", "Body fat percentage", "percent"},
	{"calories", "Caloric intake", "energy"},
	{"neck", "Neck", "length"},
	{"shoulders", "Shoulders", "length"},
	{"chest", "Chest", "length"},
	{"left_bicep", "Left bicep", "length"},
	{"right_bicep", "Right bicep", "length"},
	{"left_forearm", "Left forearm", "length"},
	{"right_forearm", "Right forearm", "length"},
	{"upper_abs", "Upper abs", "length"},
	{"waist", "Waist", "length"},
	{"lower_abs", "Lower abs", "length"},
	{"hips", "Hips", "length"},
	{"left_thigh", "Left thigh", "length"},
	{"right_thigh", "Right thigh", "length"},
	{"left_calf", "Left calf", "length"},
	{"right_calf", "Right calf", "length"},
}

// MetricEntry is one sample of a series.
type MetricEntry struct {
	ID     string
	Metric string
	At     time.Time
	Value  float64
}

// PutMetricEntry inserts or replaces a sample.
func (s *Store) PutMetricEntry(ctx context.Context, e MetricEntry) error {
	if e.ID == "" {
		e.ID = NewID()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO metric_entry (id, metric, at, value) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET metric = excluded.metric, at = excluded.at, value = excluded.value`,
		e.ID, e.Metric, Time(e.At), e.Value)
	return err
}

// DeleteMetricEntry removes a sample.
func (s *Store) DeleteMetricEntry(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM metric_entry WHERE id = ?`, id)
	return err
}

// MetricSeries returns a series newest first.
func (s *Store) MetricSeries(ctx context.Context, metric string, limit int) ([]MetricEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, metric, at, value FROM metric_entry WHERE metric = ? ORDER BY at DESC LIMIT ?`, metric, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricEntry
	for rows.Next() {
		var e MetricEntry
		var at string
		if err := rows.Scan(&e.ID, &e.Metric, &at, &e.Value); err != nil {
			return nil, err
		}
		e.At, _ = ParseTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestMetrics returns the newest sample of every series that has one.
func (s *Store) LatestMetrics(ctx context.Context) (map[string]MetricEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, metric, at, value FROM metric_entry m WHERE at = (SELECT MAX(at) FROM metric_entry WHERE metric = m.metric)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MetricEntry{}
	for rows.Next() {
		var e MetricEntry
		var at string
		if err := rows.Scan(&e.ID, &e.Metric, &at, &e.Value); err != nil {
			return nil, err
		}
		e.At, _ = ParseTime(at)
		out[e.Metric] = e
	}
	return out, rows.Err()
}
