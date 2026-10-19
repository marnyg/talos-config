package parent

import "time"

// AgeTask backdates a task's last change by d — a test's clock for
// the retry backoff, which counts from updated.
func (s *Store) AgeTask(id string, d time.Duration) error {
	return s.exec1(`UPDATE tasks SET updated = updated - ? WHERE id = ?`, int64(d.Seconds()), id)
}
