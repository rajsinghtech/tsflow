package audit

import (
	"sort"
	"strings"
)

// Category groups related events for filters and charts.
type Category string

const (
	CategoryLogins       Category = "logins"
	CategoryPolicy       Category = "policy"
	CategoryDevices      Category = "devices"
	CategoryUsers        Category = "users"
	CategoryKeys         Category = "keys"
	CategorySettings     Category = "settings"
	CategoryDNS          Category = "dns"
	CategorySharing      Category = "sharing"
	CategoryIntegrations Category = "integrations"
	CategoryTailnetLock  Category = "tailnet_lock"
	CategoryBilling      Category = "billing"
	CategoryPAM          Category = "pam"
	CategoryFailed       Category = "failed_requests"
	CategoryOther        Category = "other"
)

// CategoryInfo names a category for the UI.
type CategoryInfo struct {
	ID    Category `json:"id"`
	Label string   `json:"label"`
}

// Categories lists every category in display order.
var Categories = []CategoryInfo{
	{CategoryLogins, "Logins"},
	{CategoryPolicy, "Policy file"},
	{CategoryDevices, "Devices"},
	{CategoryUsers, "Users and groups"},
	{CategoryKeys, "Keys and credentials"},
	{CategorySettings, "Tailnet settings"},
	{CategoryDNS, "DNS"},
	{CategorySharing, "Sharing and invites"},
	{CategoryIntegrations, "Integrations"},
	{CategoryTailnetLock, "Tailnet Lock"},
	{CategoryBilling, "Billing"},
	{CategoryPAM, "PAM"},
	{CategoryFailed, "Failed requests"},
	{CategoryOther, "Other"},
}

// CatalogEntry describes one event name from Tailscale's documented list.
type CatalogEntry struct {
	Event    string   `json:"event"`
	Label    string   `json:"label"`
	Category Category `json:"category"`
}

// catalog covers every value of the event filter in the Tailscale API spec.
// Events not listed here still work; they get a label and a category from
// their target type.
var catalog = map[string]CatalogEntry{}

func init() {
	add := func(cat Category, pairs ...string) {
		for i := 0; i+1 < len(pairs); i += 2 {
			catalog[pairs[i]] = CatalogEntry{Event: pairs[i], Label: pairs[i+1], Category: cat}
		}
	}
	add(CategoryLogins,
		"NODE.LOGIN", "Device login",
		"NODE.LOGOUT", "Device logout",
		"ADMIN_CONSOLE.LOGIN", "Admin console login",
		"ADMIN_CONSOLE.LOGOUT", "Admin console logout",
		"WEB_INTERFACE.LOGIN", "Web interface login",
		"WEB_INTERFACE.LOGOUT", "Web interface logout",
	)
	add(CategoryPolicy,
		"TAILNET.UPDATE.ACL", "Policy file changed",
	)
	add(CategoryDevices,
		"NODE.APPROVE", "Device approved",
		"NODE.CREATE", "Device added",
		"NODE.CREATE.ATTRIBUTES", "Device posture attribute added",
		"NODE.DELETE", "Device removed",
		"NODE.DELETE.ATTRIBUTES", "Device posture attribute removed",
		"NODE.DISABLE.KEY_EXPIRY", "Key expiry disabled",
		"NODE.ENABLE.KEY_EXPIRY", "Key expiry enabled",
		"NODE.EXPIRED.KEY_EXPIRY_TIME", "Device key expired",
		"NODE.DISCONNECT_NODE.CLIENT_LOG", "Device disconnected",
		"NODE.REVOKE", "Device revoked",
		"NODE.UPDATE.ACL_TAGS", "Device tags changed",
		"NODE.UPDATE.ALLOWED_IPS", "Approved routes changed",
		"NODE.UPDATE.ATTRIBUTES", "Device posture attribute changed",
		"NODE.UPDATE.AUTO_APPROVED_ROUTES", "Routes auto-approved",
		"NODE.UPDATE.EXIT_NODE", "Exit node setting changed",
		"NODE.UPDATE.KEY_EXPIRY_TIME", "Key expiry time changed",
		"NODE.UPDATE.MACHINE_NAME", "Device renamed",
		"NODE.UPDATE.POSTURE_IDENTITY", "Device posture identity changed",
	)
	add(CategoryTailnetLock,
		"NODE.UPDATE.TKA", "Device key signed (Tailnet Lock)",
		"TAILNET.CREATE.TKA", "Tailnet Lock key added",
		"TAILNET.DELETE.TKA", "Tailnet Lock key removed",
		"TAILNET.UPDATE.TKA", "Tailnet Lock key updated",
		"TAILNET.ENABLE.TKA", "Tailnet Lock enabled",
		"TAILNET.DISABLE.TKA", "Tailnet Lock disabled",
	)
	add(CategoryUsers,
		"USER.APPROVE", "User approved",
		"USER.CREATE", "User added",
		"USER.DELETE", "User deleted",
		"USER.INVITE", "User invited",
		"USER.PUSH_USER.ATTRIBUTES", "User updated by SCIM",
		"USER.RESEND.TAILNET_INVITE", "User invite resent",
		"USER.RESTORE", "User restored",
		"USER.RESTORE_GLOBAL", "User restored (all tailnets)",
		"USER.SUSPEND", "User suspended",
		"USER.SUSPEND_GLOBAL", "User suspended (all tailnets)",
		"USER.UPDATE.USER_ROLE", "User role changed",
		"GROUP.PUSH_GROUP.ATTRIBUTES", "Group updated by SCIM",
		"GROUP.UPDATE.USER_ROLE", "Group role changed",
		"TAILNET.JOIN", "User joined another tailnet",
		"TAILNET.LEAVE", "User left another tailnet",
	)
	add(CategoryKeys,
		"API_KEY.CREATE", "Key created",
		"API_KEY.EXPIRED", "Key expired",
		"API_KEY.REVOKE", "Key revoked",
		"API_KEY.UPDATE", "Key updated",
	)
	add(CategorySettings,
		"TAILNET.CREATE", "Tailnet created",
		"TAILNET.ACCEPT.FEATURE", "Feature invite accepted",
		"TAILNET.JOIN_WAITLIST.FEATURE", "Feature waitlist joined",
		"TAILNET.UPDATE.MAX_KEY_DURATION", "Max key duration changed",
		"TAILNET.UPDATE.ROUTE_SELECTION", "Route selection changed",
		"TAILNET.UPDATE.TCD", "Tailnet DNS name changed",
		"TAILNET.UPDATE.ACCOUNT_EMAIL", "Account email changed",
		"TAILNET.UPDATE.SECURITY_EMAIL", "Security email changed",
		"TAILNET.UPDATE.SUPPORT_EMAIL", "Configuration email changed",
		"TAILNET.VERIFY.ACCOUNT_EMAIL", "Account email verified",
		"TAILNET.VERIFY.SECURITY_EMAIL", "Security email verified",
		"TAILNET.VERIFY.SUPPORT_EMAIL", "Configuration email verified",
	)
	for prop, name := range map[string]string{
		"COLLECT_POSTURE_IDENTITY": "Posture identity collection",
		"COLLECT_SERVICES":         "Services collection",
		"FILE_SHARING":             "Taildrop",
		"GEOSTEERING":              "Geosteering",
		"HTTPS":                    "HTTPS certificates",
		"LOG_EXIT_FLOWS":           "Exit node flow logging",
		"MACHINE_APPROVAL_NEEDED":  "Device approval",
		"MULLVAD_VPN":              "Mullvad exit nodes",
		"NETWORK_FLOW_LOGGING":     "Network flow logging",
		"SCIM":                     "User and group provisioning",
		"USER_APPROVAL_REQUIRED":   "User approval",
	} {
		add(CategorySettings, "TAILNET.ENABLE."+prop, name+" enabled", "TAILNET.DISABLE."+prop, name+" disabled")
	}
	add(CategoryDNS,
		"TAILNET.UPDATE.DNS_CONFIG", "DNS settings changed",
		"TAILNET.ENABLE.MAGIC_DNS", "MagicDNS enabled",
		"TAILNET.DISABLE.MAGIC_DNS", "MagicDNS disabled",
	)
	add(CategorySharing,
		"INVITE.ACCEPT.FEATURE", "Feature invite accepted",
		"INVITE.ACCEPT.NODE_SHARE", "Device share invite accepted",
		"INVITE.ACCEPT.TAILNET_INVITE", "Tailnet invite accepted",
		"INVITE.CREATE.FEATURE", "Feature invite created",
		"INVITE.CREATE.NODE_SHARE", "Device share invite created",
		"INVITE.CREATE.TAILNET_INVITE", "Tailnet invite created",
		"INVITE.DELETE.NODE_SHARE", "Device share invite revoked",
		"INVITE.DELETE.TAILNET_INVITE", "Tailnet invite revoked",
		"INVITE.RESEND.NODE_SHARE", "Device share invite resent",
		"INVITE.RESEND.TAILNET_INVITE", "Tailnet invite resent",
		"SHARE.CREATE", "Device shared",
		"SHARE.DELETE", "Device share removed",
		"SHARE.UPDATE", "Device share updated",
	)
	add(CategoryIntegrations,
		"TAILNET.CREATE.LOGSTREAM_ENDPOINT", "Log streaming endpoint added",
		"TAILNET.DELETE.LOGSTREAM_ENDPOINT", "Log streaming endpoint removed",
		"TAILNET.UPDATE.LOGSTREAM_ENDPOINT", "Log streaming endpoint changed",
		"TAILNET.CREATE.POSTURE_INTEGRATION", "Posture integration added",
		"TAILNET.DELETE.POSTURE_INTEGRATION", "Posture integration removed",
		"TAILNET.UPDATE.POSTURE_INTEGRATION", "Posture integration changed",
		"WEBHOOK_ENDPOINT.CREATE", "Webhook added",
		"WEBHOOK_ENDPOINT.DELETE", "Webhook removed",
		"WEBHOOK_ENDPOINT.UPDATE.SECRET", "Webhook secret rotated",
		"WEBHOOK_ENDPOINT.UPDATE.SUBSCRIBED_EVENTS", "Webhook events changed",
	)
	add(CategoryBilling,
		"BILLING.CANCEL.SUBSCRIPTION", "Subscription canceled",
		"BILLING.CREATE.SUBSCRIPTION", "Subscription created",
		"BILLING.UPDATE.ADDRESS", "Billing address changed",
		"BILLING.UPDATE.BILLING_OWNER", "Billing owner changed",
		"BILLING.UPDATE.EMAIL", "Billing email changed",
		"BILLING.UPDATE.PAYMENT_INFO", "Payment details changed",
		"BILLING.UPDATE.STRIPE_CUSTOMER_ID", "Billing customer changed",
		"BILLING.UPDATE.SUBSCRIPTION", "Subscription changed",
	)
	add(CategoryPAM,
		"PAM_CONNECTOR.CREATE", "PAM connector added",
		"PAM_CONNECTOR.CREATE.ACCESS_TOKEN", "PAM connector token created",
		"PAM_CONNECTOR.DELETE", "PAM connector removed",
		"PAM_CONNECTOR.DISABLE.ACCESS_TOKEN", "PAM connector token disabled",
		"PAM_CONNECTOR.UPDATE", "PAM connector changed",
		"PAM_SERVICE.CREATE", "PAM service added",
		"PAM_SERVICE.DELETE", "PAM service removed",
		"PAM_SERVICE.UPDATE", "PAM service changed",
		"PAM_SERVICE_ACCOUNT.CREATE", "PAM service account added",
		"PAM_SERVICE_ACCOUNT.CREATE.ACCESS_TOKEN", "PAM service account token created",
		"PAM_SERVICE_ACCOUNT.DELETE", "PAM service account removed",
		"PAM_SERVICE_ACCOUNT.UPDATE", "PAM service account changed",
		"PAM_SETTINGS.CREATE.CUSTOM_DOMAIN", "PAM custom domain added",
		"PAM_SETTINGS.CREATE.NOTIFICATION", "PAM notification added",
		"PAM_SETTINGS.CREATE.RECORDING_STORAGE", "PAM recording storage added",
		"PAM_SETTINGS.DELETE.CUSTOM_DOMAIN", "PAM custom domain removed",
		"PAM_SETTINGS.DELETE.NOTIFICATION", "PAM notification removed",
		"PAM_SETTINGS.DELETE.RECORDING_STORAGE", "PAM recording storage removed",
		"PAM_SETTINGS.UPDATE", "PAM settings changed",
		"PAM_SETTINGS.UPDATE.CUSTOM_DOMAIN", "PAM custom domain changed",
		"PAM_SETTINGS.UPDATE.NOTIFICATION", "PAM notification changed",
		"PAM_SETTINGS.UPDATE.SETUP_WIZARD", "PAM setup changed",
	)
	add(CategoryFailed,
		"FAILED_REQUEST.UPDATE", "Request denied (insufficient permission)",
	)
}

// targetCategories places events missing from the catalog.
var targetCategories = map[string]Category{
	"NODE":             CategoryDevices,
	"USER":             CategoryUsers,
	"GROUP":            CategoryUsers,
	"API_KEY":          CategoryKeys,
	"INVITE":           CategorySharing,
	"SHARE":            CategorySharing,
	"BILLING":          CategoryBilling,
	"ADMIN_CONSOLE":    CategoryLogins,
	"WEB_INTERFACE":    CategoryLogins,
	"WEBHOOK_ENDPOINT": CategoryIntegrations,
	"FAILED_REQUEST":   CategoryFailed,
	"TAILNET":          CategorySettings,
}

// Describe returns the catalog entry for an event name. Unknown names get a
// readable label built from the name and a category from the target type.
func Describe(event string) CatalogEntry {
	event = strings.ToUpper(strings.TrimSpace(event))
	if e, ok := catalog[event]; ok {
		return e
	}
	parts := strings.Split(event, ".")
	cat := CategoryOther
	if len(parts) > 0 {
		if strings.HasPrefix(parts[0], "PAM_") {
			cat = CategoryPAM
		} else if c, ok := targetCategories[parts[0]]; ok {
			cat = c
		}
	}
	if len(parts) >= 2 && parts[1] == "LOGIN" || len(parts) >= 2 && parts[1] == "LOGOUT" {
		cat = CategoryLogins
	}
	words := make([]string, 0, len(parts))
	for i, p := range parts {
		w := strings.ToLower(strings.ReplaceAll(p, "_", " "))
		if i == 0 && w != "" {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		words = append(words, w)
	}
	return CatalogEntry{Event: event, Label: strings.Join(words, " "), Category: cat}
}

// CategoryOf returns the category for an event name.
func CategoryOf(event string) Category { return Describe(event).Category }

// Catalog returns every known event, sorted by category order then name.
func Catalog() []CatalogEntry {
	order := map[Category]int{}
	for i, c := range Categories {
		order[c.ID] = i
	}
	out := make([]CatalogEntry, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Category] != order[out[j].Category] {
			return order[out[i].Category] < order[out[j].Category]
		}
		return out[i].Event < out[j].Event
	})
	return out
}

// KnownCategory reports whether id names a category.
func KnownCategory(id string) bool {
	for _, c := range Categories {
		if string(c.ID) == id {
			return true
		}
	}
	return false
}
