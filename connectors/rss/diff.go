package rss

import "github.com/mmcdole/gofeed"

// diffItems returns the items considered "new" relative to the watermark,
// plus the new watermark to persist.
//
// Convention: feed.Items are typically newest-first. We walk in that order
// looking for the watermark id; everything *above* it is new. We return
// the new items in feed-order (newest first) for natural CLI display.
//
// Edge cases:
//   - watermark == "" (first run): if includeBacklog, return everything;
//     otherwise return [] and set watermark to the newest item.
//   - watermark not found in current items: treat all as new (the feed
//     may have rolled past since last fetch). If this turns out to flood,
//     we can add a "max_emit" cap.
func diffItems(feed *gofeed.Feed, watermark string, includeBacklog bool) ([]*gofeed.Item, string) {
	if feed == nil || len(feed.Items) == 0 {
		return nil, watermark
	}
	newestID := itemID(feed.Items[0])

	if watermark == "" {
		if includeBacklog {
			return feed.Items, newestID
		}
		// First run — silently advance, emit nothing.
		return nil, newestID
	}

	// Walk newest-first; stop at the watermark.
	var fresh []*gofeed.Item
	for _, it := range feed.Items {
		id := itemID(it)
		if id == watermark {
			break
		}
		fresh = append(fresh, it)
	}
	if len(fresh) == 0 {
		return nil, watermark
	}
	return fresh, newestID
}
