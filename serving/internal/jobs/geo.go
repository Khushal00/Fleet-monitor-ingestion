package jobs

import "math"

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// pointToSegmentDistanceKm returns the shortest distance from point P
// to the line segment AB (all in degrees, result in km).
func pointToSegmentDistanceKm(pLat, pLng, aLat, aLng, bLat, bLng float64) float64 {
	if aLat == bLat && aLng == bLng {
		return haversineKm(pLat, pLng, aLat, aLng)
	}
	t := ((pLat-aLat)*(bLat-aLat) + (pLng-aLng)*(bLng-aLng)) /
		((bLat-aLat)*(bLat-aLat) + (bLng-aLng)*(bLng-aLng))
	t = math.Max(0, math.Min(1, t))
	return haversineKm(pLat, pLng, aLat+t*(bLat-aLat), aLng+t*(bLng-aLng))
}
