package multichannel

import (
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// WaypointTrajectory interpolates only catalog embedding evidence. It does not
// interpret MusicIntent.Journey.EnergyTrajectory as acoustic energy.
type WaypointTrajectory struct {
	cat       ports.Catalog
	waypoints []core.TrackRef
}

func NewWaypointTrajectory(cat ports.Catalog, waypoints []core.TrackRef) *WaypointTrajectory {
	return &WaypointTrajectory{cat: cat, waypoints: append([]core.TrackRef(nil), waypoints...)}
}

func (t *WaypointTrajectory) Target(position float64) (ports.Vectors, bool) {
	if len(t.waypoints) < 2 {
		return ports.Vectors{}, false
	}
	position = clamp(position, 0, 1)
	scaled := position * float64(len(t.waypoints)-1)
	segment := minInt(int(math.Floor(scaled)), len(t.waypoints)-2)
	local := scaled - float64(segment)
	start, startOK := t.cat.Vectors(t.waypoints[segment].ID)
	end, endOK := t.cat.Vectors(t.waypoints[segment+1].ID)
	if !startOK || !endOK {
		return ports.Vectors{}, false
	}
	return ports.Vectors{
		Audio: interpolate(start.Audio, end.Audio, local),
		Track: interpolate(start.Track, end.Track, local),
	}, true
}

func (*WaypointTrajectory) Evidence() string {
	return "piecewise interpolation of catalog audio and co-occurrence embeddings"
}

var _ ports.Trajectory = (*WaypointTrajectory)(nil)
