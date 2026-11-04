package main

import (
	"os"
	"strings"
)

// langCode is the active output language: "ru" or "en".
var langCode = "ru"

// L picks a string for the active language. Keeping both variants inline makes
// translations reviewable next to the code that uses them.
func L(ru, en string) string {
	if langCode == "en" {
		return en
	}
	return ru
}

// LocaleInfo carries the detected system language and keyboard layouts.
type LocaleInfo struct {
	LangName string // human-readable language name
	LangCode string // ru / en / ...
	Layouts  string // e.g. "us, ru"
	Variant  string
	Source   string // where the language was detected from
}

// detectLanguage resolves the UI language from the environment, then from
// system configuration files. Russian locales select ru, anything else en.
func detectLanguage() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return langFromLocale(v)
		}
	}
	if m := readKeyValueFile("/etc/default/locale", "="); len(m) > 0 {
		for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			if v := strings.Trim(m[k], "\""); v != "" {
				return langFromLocale(v)
			}
		}
	}
	if out, err := runCmd(cmdTimeout, "localectl", "status"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "System Locale:") {
				fields := strings.Fields(strings.TrimPrefix(line, "System Locale:"))
				for _, f := range fields {
					if idx := strings.Index(f, "="); idx >= 0 {
						if lang := langFromLocale(f[idx+1:]); lang != "" {
							return lang
						}
					}
				}
			}
		}
	}
	return "ru"
}

func langFromLocale(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimSuffix(v, ".utf-8")
	v = strings.TrimSuffix(v, ".utf8")
	switch {
	case v == "":
		return ""
	case strings.HasPrefix(v, "ru"):
		return "ru"
	case v == "c" || v == "posix":
		return "en"
	default:
		return "en"
	}
}

// collectLocale reports the system language and keyboard layouts. Both are
// optional: on a headless box with no configuration we simply show less.
func collectLocale() LocaleInfo {
	li := LocaleInfo{}

	// Language: prefer the explicit locale env/config, else the config file.
	locale := ""
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			locale = v
			li.Source = "$" + k
			break
		}
	}
	if locale == "" {
		if m := readKeyValueFile("/etc/default/locale", "="); len(m) > 0 {
			for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
				if v := strings.Trim(m[k], "\""); v != "" {
					locale = v
					li.Source = "/etc/default/locale"
					break
				}
			}
		}
	}
	li.LangCode = langFromLocale(locale)
	li.LangName = languageName(li.LangCode)

	// Keyboard layouts: /etc/default/keyboard is the Debian/RPiOS source.
	km := readKeyValueFile("/etc/default/keyboard", "=")
	li.Layouts = strings.Trim(km["XKBLAYOUT"], "\"")
	li.Variant = strings.Trim(km["XKBVARIANT"], "\"")

	// Fall back to localectl (systemd) for desktop installs.
	if li.Layouts == "" {
		if out, err := runCmd(cmdTimeout, "localectl", "status"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(line, "X11 Layout:"):
					li.Layouts = strings.TrimSpace(strings.TrimPrefix(line, "X11 Layout:"))
				case strings.HasPrefix(line, "X11 Variant:") && li.Variant == "":
					li.Variant = strings.TrimSpace(strings.TrimPrefix(line, "X11 Variant:"))
				}
			}
		}
	}
	return li
}

// languageName maps a language code to a display name in the active language.
func languageName(code string) string {
	switch code {
	case "ru":
		return L("русский", "Russian")
	case "en":
		return L("английский", "English")
	case "de":
		return L("немецкий", "German")
	case "fr":
		return L("французский", "French")
	case "es":
		return L("испанский", "Spanish")
	case "zh":
		return L("китайский", "Chinese")
	case "ja":
		return L("японский", "Japanese")
	case "":
		return ""
	}
	return code
}
