package config

// Settings lookups are injected at runtime by bootstrap (after DB init)
// to keep the config package free of a `model/data` import, which would
// create a cycle via `model/dao -> config`.
//
// If unset (e.g. during early startup or tests) the getters return the
// zero string / 0 and callers fall through to the .env / default path.

// SettingsGetString is installed by bootstrap.Init with a closure that
// reads from the settings table. Runtime-only.
var SettingsGetString func(key string) string

func settingsRateApiUrl() string {
	if SettingsGetString == nil {
		return ""
	}
	return SettingsGetString("rate.api_url")
}

func settingsForcedUsdtRate() float64 {
	if SettingsGetString == nil {
		return 0
	}
	raw := SettingsGetString("rate.forced_usdt_rate")
	if raw == "" {
		return 0
	}
	f, err := parseFloat(raw)
	if err != nil {
		return 0
	}
	return f
}
