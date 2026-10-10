package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const dashboardFeriasItemURL = "https://ferias.bulzipke.com/sozai/item.js"
const dashboardFeriasPageURL = "https://ferias.bulzipke.com/sozai/itemlist.htm"
const dashboardDeliveryCatalogTTL = 2 * time.Minute

type dashboardDeliveryItem struct {
	ID   uint16 `json:"id"`
	Hex  string `json:"hex"`
	Name string `json:"name"`
}

// Names come from the same live data used by itemlist.htm. They must not be
// embedded into the server's immutable, language-independent item snapshot.
type dashboardDeliveryCatalog struct {
	mu       sync.Mutex
	items    []dashboardDeliveryItem
	loadedAt time.Time
	fetch    func(context.Context) ([]dashboardDeliveryItem, error)
}

func (c *dashboardDeliveryCatalog) load(ctx context.Context, refresh bool) ([]dashboardDeliveryItem, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	if !refresh && len(c.items) > 0 && time.Since(c.loadedAt) < dashboardDeliveryCatalogTTL {
		return c.items, c.loadedAt, nil
	}
	fetch := c.fetch
	if fetch == nil {
		fetch = fetchDashboardDeliveryCatalog
	}
	items, err := fetch(ctx)
	if err != nil {
		// Never silently distribute against an old/partial list after a refresh fails.
		c.loadedAt = time.Time{}
		return nil, time.Time{}, err
	}
	c.items, c.loadedAt = items, time.Now()
	return c.items, c.loadedAt, nil
}

func fetchDashboardDeliveryCatalog(ctx context.Context) ([]dashboardDeliveryItem, error) {
	client := &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return fmt.Errorf("Ferias catalog redirects are not allowed")
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, dashboardFeriasItemURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ferias catalog returned HTTP %d", response.StatusCode)
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("Ferias catalog exceeds size limit")
	}
	items, err := parseDashboardDeliveryCatalog(data)
	if err != nil {
		return nil, err
	}
	if len(items) < 1000 {
		return nil, fmt.Errorf("Ferias catalog is unexpectedly incomplete: %d items", len(items))
	}
	return items, nil
}

// Extract the JSON-compatible object literal; never execute upstream JavaScript.
func parseDashboardDeliveryCatalog(data []byte) ([]dashboardDeliveryItem, error) {
	source := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	start, end := strings.Index(source, "return {"), strings.LastIndex(source, "};};")
	if start < 0 || end < start {
		return nil, fmt.Errorf("unrecognized Ferias item.js format")
	}
	var rows map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(source[start+len("return "):end+1]), &rows); err != nil {
		return nil, fmt.Errorf("decode Ferias items: %w", err)
	}
	items := make([]dashboardDeliveryItem, 0, len(rows))
	for key, row := range rows {
		id, err := strconv.ParseUint(key, 16, 16)
		if err != nil || len(key) != 4 || id == 0 || len(row) < 2 {
			return nil, fmt.Errorf("invalid Ferias item ID or row: %q", key)
		}
		// Match itemlist.htm's exclusion of rows whose rarity is "-".
		if string(row[1]) == `"-"` {
			continue
		}
		var name string
		if err := json.Unmarshal(row[0], &name); err != nil {
			return nil, fmt.Errorf("invalid Ferias item name for %s", key)
		}
		name = strings.TrimSpace(name)
		if name == "" || utf8.RuneCountInString(name) > 160 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("invalid Ferias item name for %s", key)
		}
		items = append(items, dashboardDeliveryItem{ID: uint16(id), Hex: fmt.Sprintf("%04X", id), Name: name})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func deliverySearchName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(value)), ""))
}

func searchDashboardDeliveryItems(items []dashboardDeliveryItem, query string) []dashboardDeliveryItem {
	result := make([]dashboardDeliveryItem, 0, 20)
	query = strings.TrimSpace(query)
	if query == "" {
		return result
	}
	decimalID, decimalErr := strconv.ParseUint(query, 10, 16)
	hexQuery := strings.TrimPrefix(strings.ToLower(query), "0x")
	hexID, hexErr := strconv.ParseUint(hexQuery, 16, 16)
	for _, item := range items {
		if (decimalErr == nil && uint64(item.ID) == decimalID) ||
			(hexErr == nil && (len(hexQuery) == 4 || strings.HasPrefix(strings.ToLower(query), "0x")) && uint64(item.ID) == hexID) {
			result = append(result, item)
		}
	}
	words := strings.Fields(norm.NFKC.String(query))
	for _, item := range items {
		if len(result) >= 20 {
			break
		}
		exact := false
		for _, match := range result {
			if match.ID == item.ID {
				exact = true
				break
			}
		}
		if exact {
			continue
		}
		name, matches := deliverySearchName(item.Name), true
		for _, word := range words {
			if !strings.Contains(name, deliverySearchName(word)) {
				matches = false
				break
			}
		}
		if matches {
			result = append(result, item)
		}
	}
	return result
}
