package repo

import "time"

type ResourceBucket struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryPercent float64 `json:"memoryPercent"`
}

const timestampLayout = "2006-01-02T15:04:05.000Z"

func (r *Repo) UpdateServiceStatus(id, status string) error {
	_, err := r.db.Exec(`UPDATE services SET status = ?, updated_at = ? WHERE id = ?`,
		status, nowTimestamp(), id)
	return err
}

func (r *Repo) AddHealthHistory(serviceID, status string) error {
	_, err := r.db.Exec(`INSERT INTO service_health_history (id, service_id, status, checked_at) VALUES (?, ?, ?, ?)`,
		newID(), serviceID, status, nowTimestamp())
	return err
}

func (r *Repo) AddResourceHistory(serviceID string, cpuPercent, memoryPercent float64) error {
	_, err := r.db.Exec(`INSERT INTO service_resource_history (id, service_id, cpu_percent, memory_percent, checked_at) VALUES (?, ?, ?, ?, ?)`,
		newID(), serviceID, cpuPercent, memoryPercent, nowTimestamp())
	return err
}

func (r *Repo) HealthHistory(serviceID string, days, buckets int) []any {
	result := make([]any, buckets)
	if buckets <= 0 || days <= 0 {
		return result
	}
	start := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	cutoff := start.Format(timestampLayout)
	rows, err := r.db.Query(`SELECT status, checked_at FROM service_health_history
		WHERE service_id = ? AND checked_at >= ? ORDER BY checked_at ASC`, serviceID, cutoff)
	if err != nil {
		return result
	}
	defer rows.Close()
	type healthSeen struct{ up, down, unknown bool }
	seen := make([]healthSeen, buckets)
	for rows.Next() {
		var status, raw string
		if err := rows.Scan(&status, &raw); err != nil {
			continue
		}
		checkedAt, err := time.Parse(timestampLayout, raw)
		if err != nil {
			continue
		}
		index := int(float64(checkedAt.Sub(start)) / float64(time.Duration(days)*24*time.Hour) * float64(buckets))
		if index < 0 || index >= buckets {
			continue
		}
		switch status {
		case "up":
			seen[index].up = true
		case "down":
			seen[index].down = true
		default:
			seen[index].unknown = true
		}
	}
	for index, bucket := range seen {
		if !bucket.up && !bucket.down && !bucket.unknown {
			continue
		}
		switch {
		case bucket.up && bucket.down:
			result[index] = "mixed"
		case bucket.down:
			result[index] = "down"
		case bucket.unknown:
			result[index] = "unknown"
		default:
			result[index] = "up"
		}
	}
	return result
}

func (r *Repo) ResourceHistory(serviceID string, days, buckets int) []*ResourceBucket {
	result := make([]*ResourceBucket, buckets)
	if buckets <= 0 || days <= 0 {
		return result
	}
	start := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	cutoff := start.Format(timestampLayout)
	rows, err := r.db.Query(`SELECT cpu_percent, memory_percent, checked_at FROM service_resource_history
		WHERE service_id = ? AND checked_at >= ? ORDER BY checked_at ASC`, serviceID, cutoff)
	if err != nil {
		return result
	}
	defer rows.Close()
	sums := make([]ResourceBucket, buckets)
	counts := make([]int, buckets)
	for rows.Next() {
		var cpu, memory float64
		var raw string
		if err := rows.Scan(&cpu, &memory, &raw); err != nil {
			continue
		}
		checkedAt, err := time.Parse(timestampLayout, raw)
		if err != nil {
			continue
		}
		index := int(float64(checkedAt.Sub(start)) / float64(time.Duration(days)*24*time.Hour) * float64(buckets))
		if index < 0 || index >= buckets {
			continue
		}
		sums[index].CPUPercent += cpu
		sums[index].MemoryPercent += memory
		counts[index]++
	}
	for index := range counts {
		if counts[index] == 0 {
			continue
		}
		value := sums[index]
		value.CPUPercent /= float64(counts[index])
		value.MemoryPercent /= float64(counts[index])
		result[index] = &value
	}
	return result
}
