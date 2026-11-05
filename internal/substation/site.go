package substation

import (
	"fmt"
	"unicode/utf8"
)

// SiteSettings are the substation settings of a deployment.
type SiteSettings struct {
	// FindingPriority is the least severe priority that opens a finding (1–4). Readings rated less
	// severe are kept with their priority but open nothing: NETA's priority 4 (a rise of 1 °C over
	// a similar component) is within the error of most cameras.
	FindingPriority int `json:"finding_priority"`
	// AlertPriority is the least severe priority sent to Telegram when a finding opens or gets
	// worse (1–4), 0 for none.
	AlertPriority int `json:"alert_priority"`
	// PD are the partial discharge levels of points that do not set their own.
	PD PDLevels `json:"pd"`
	// TelegramChatID receives the alerts; empty: the server's default chat.
	TelegramChatID string `json:"telegram_chat_id"`
}

// DefaultSiteSettings apply until an administrator saves the settings.
var DefaultSiteSettings = SiteSettings{FindingPriority: P3, AlertPriority: P2, PD: DefaultPDLevels}

// Check validates the settings.
func (s SiteSettings) Check() error {
	switch {
	case s.FindingPriority < P1 || s.FindingPriority > P4:
		return fmt.Errorf("finding_priority: 1 to 4")
	case s.AlertPriority < None || s.AlertPriority > P4:
		return fmt.Errorf("alert_priority: 0 (none) or 1 to 4")
	case utf8.RuneCountInString(s.TelegramChatID) > 64:
		return fmt.Errorf("telegram_chat_id: up to 64 characters")
	}
	return CheckLevels(s.PD)
}

// Opens reports whether a priority opens a finding.
func (s SiteSettings) Opens(p int) bool { return p != None && p <= s.FindingPriority }

// Alerts reports whether a priority is sent to Telegram.
func (s SiteSettings) Alerts(p int) bool { return p != None && p <= s.AlertPriority }
