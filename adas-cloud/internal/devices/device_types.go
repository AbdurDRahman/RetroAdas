package devices

// The two kinds of device the system knows (docs/design.md section 3). The
// devices table accepts only these (a check in migration 001), and the type
// decides which hazards a device may report and whether its reports count as
// "auto" or "manual" (internal/hazards, the event-tag registry).
//
// Named constants instead of the text typed out in several places, so a
// misspelling is a build error instead of a rule that silently never matches.
const (
	DeviceTypeJetsonUnit = "jetson_unit"
	DeviceTypeMobileApp  = "mobile_app"
)
