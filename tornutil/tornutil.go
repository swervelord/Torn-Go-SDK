// Package tornutil holds Torn helpers (PARITY_SPEC P8.6): links to Torn pages
// and conversions between Torn timestamps and time.Time. Torn City Time (TCT)
// is UTC.
package tornutil

import (
	"net/url"
	"strconv"
	"time"
)

// BaseURL is the Torn website origin used by the link builders.
const BaseURL = "https://www.torn.com"

func id(n int64) string { return strconv.FormatInt(n, 10) }

// ProfileURL links to a player's profile.
func ProfileURL(userID int64) string { return BaseURL + "/profiles.php?XID=" + id(userID) }

// AttackURL links to the attack page for a player.
func AttackURL(userID int64) string {
	return BaseURL + "/loader.php?sid=attack&user2ID=" + id(userID)
}

// FactionURL links to a faction's profile.
func FactionURL(factionID int64) string {
	return BaseURL + "/factions.php?step=profile&ID=" + id(factionID)
}

// CompanyURL links to a company's profile.
func CompanyURL(companyID int64) string {
	return BaseURL + "/joblist.php#/p=corpinfo&ID=" + id(companyID)
}

// BazaarURL links to a player's bazaar.
func BazaarURL(userID int64) string { return BaseURL + "/bazaar.php?userId=" + id(userID) }

// ItemMarketURL links to the item market listing of an item.
func ItemMarketURL(itemID int64) string {
	return BaseURL + "/page.php?sid=ItemMarket#/market/view=search&itemID=" + id(itemID)
}

// TradeURL links to a trade.
func TradeURL(tradeID int64) string { return BaseURL + "/trade.php#step=view&ID=" + id(tradeID) }

// ForumThreadURL links to a forum thread in a forum category.
func ForumThreadURL(forumID, threadID int64) string {
	return BaseURL + "/forums.php#/p=threads&f=" + id(forumID) + "&t=" + id(threadID)
}

// PropertyURL links to a property.
func PropertyURL(propertyID int64) string {
	return BaseURL + "/properties.php#/p=propertyinfo&ID=" + id(propertyID)
}

// SearchURL links to the player search for a name.
func SearchURL(name string) string {
	return BaseURL + "/page.php?sid=UserList&searchname=" + url.QueryEscape(name)
}

// FromTimestamp converts a Torn Unix timestamp (seconds) to a UTC time. Zero
// means "never" in many Torn fields and maps to the zero time.Time.
func FromTimestamp(ts int64) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0).UTC()
}

// ToTimestamp converts a time to a Torn Unix timestamp (seconds); the zero
// time maps to 0.
func ToTimestamp(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// TCT returns t in Torn City Time (UTC).
func TCT(t time.Time) time.Time { return t.UTC() }

// NowTCT returns the current Torn City Time.
func NowTCT() time.Time { return time.Now().UTC() }

// FormatTCT formats t as Torn displays it, "15:04:05 - 02/01/06" in TCT.
func FormatTCT(t time.Time) string { return t.UTC().Format("15:04:05 - 02/01/06") }

// Until returns the duration from now until a Torn timestamp (negative when past).
func Until(ts int64, now time.Time) time.Duration { return FromTimestamp(ts).Sub(now) }
