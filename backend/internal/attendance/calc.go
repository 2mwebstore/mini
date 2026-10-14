package attendance

import (
	"math"
	"time"

	"attendance/backend/internal/models"
)

// WorkingMinutes is the plain elapsed time between check-in and check-out.
func WorkingMinutes(in, out time.Time) int {
	if !out.After(in) {
		return 0
	}
	return int(out.Sub(in) / time.Minute)
}

// DeriveStatus picks the day's headline status. Late and early-leave minutes are
// always stored separately, so a day that is both still reports both in totals.
func DeriveStatus(lateMinutes, earlyMinutes int) string {
	switch {
	case lateMinutes > 0:
		return models.StatusLate
	case earlyMinutes > 0:
		return models.StatusEarlyLeave
	default:
		return models.StatusPresent
	}
}

// DeriveConfirmation folds the per-event reviews into the attendance-level status.
func DeriveConfirmation(confs []models.AttendanceConfirmation) string {
	if len(confs) == 0 {
		return models.ConfConfirmed
	}
	pending := false
	for _, c := range confs {
		switch c.Status {
		case models.ConfRejected:
			return models.ConfRejected
		case models.ConfPending:
			pending = true
		}
	}
	if pending {
		return models.ConfPending
	}
	return models.ConfConfirmed
}

// DistanceMeters is the great-circle distance between two coordinates.
func DistanceMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}
