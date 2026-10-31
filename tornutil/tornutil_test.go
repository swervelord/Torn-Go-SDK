package tornutil

import (
	"testing"
	"time"
)

func TestLinks(t *testing.T) {
	for got, want := range map[string]string{
		ProfileURL(4):        "https://www.torn.com/profiles.php?XID=4",
		AttackURL(4):         "https://www.torn.com/loader.php?sid=attack&user2ID=4",
		FactionURL(9):        "https://www.torn.com/factions.php?step=profile&ID=9",
		CompanyURL(5):        "https://www.torn.com/joblist.php#/p=corpinfo&ID=5",
		BazaarURL(4):         "https://www.torn.com/bazaar.php?userId=4",
		ItemMarketURL(206):   "https://www.torn.com/page.php?sid=ItemMarket#/market/view=search&itemID=206",
		TradeURL(7):          "https://www.torn.com/trade.php#step=view&ID=7",
		ForumThreadURL(2, 3): "https://www.torn.com/forums.php#/p=threads&f=2&t=3",
		PropertyURL(8):       "https://www.torn.com/properties.php#/p=propertyinfo&ID=8",
		SearchURL("a b"):     "https://www.torn.com/page.php?sid=UserList&searchname=a+b",
	} {
		if got != want {
			t.Errorf("%s != %s", got, want)
		}
	}
}

func TestTimestamps(t *testing.T) {
	ts := int64(1_700_000_000)
	if got := FromTimestamp(ts); got.Location() != time.UTC || ToTimestamp(got) != ts {
		t.Fatal(got)
	}
	if !FromTimestamp(0).IsZero() || ToTimestamp(time.Time{}) != 0 {
		t.Fatal("zero")
	}
	local := time.Date(2026, 10, 31, 1, 2, 3, 0, time.FixedZone("x", 3600))
	if FormatTCT(local) != "00:02:03 - 31/10/26" || TCT(local).Location() != time.UTC {
		t.Fatal(FormatTCT(local))
	}
	if Until(ts+60, time.Unix(ts, 0)) != time.Minute {
		t.Fatal("until")
	}
}
