package sfu

import "time"

const (
	quarterResolution = "q"
	halfResolution    = "h"
	fullResolution    = "f"
)

type SimulcastConfig struct {
	BestQualityFirst    bool `mapstructure:"bestqualityfirst"`
	EnableTemporalLayer bool `mapstructure:"enabletemporallayer"`
}

type simulcastTrackHelpers struct {
	targetSpatialLayer int
	temporalSupported  bool
	targetTempLayer    int
	currentTempLayer   int
	temporalEnabled    bool
	lTSCalc            time.Time

	// VP8Helper temporal helpers
	refPicID  uint16
	lastPicID uint16
	refTlzi   uint8
	lastTlzi  uint8
}
