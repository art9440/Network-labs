package wiki

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/art9440/Network-labs/lab3/internal/domain/httpx"
	"github.com/art9440/Network-labs/lab3/internal/shared/result"
)

const baseURL = "https://en.wikipedia.org/w/api.php?"

type WikiLocation struct {
	out chan result.Result[[]Location]
}

type Location struct {
	ID      string // pageid
	Name    string // title
	Summary string // краткое описание (intro)
	URL     string // канонический урл страницы
}

// --- RAW: геопоиск ---
type wikiGeoResp struct {
	Query struct {
		Geosearch []struct {
			PageID int    `json:"pageid"`
			Title  string `json:"title"`
		} `json:"geosearch"`
	} `json:"query"`
}

// --- RAW: детали по набору pageids ---
type wikiDetailsResp struct {
	Query struct {
		Pages map[string]struct {
			PageID  int    `json:"pageid"`
			Title   string `json:"title"`
			Extract string `json:"extract"`
			FullURL string `json:"fullurl"`
		} `json:"pages"`
	} `json:"query"`
}

func NewWikiLocation(out chan result.Result[[]Location]) *WikiLocation {
	return &WikiLocation{out: out}
}

func (w WikiLocation) HttpWikiLocation(ctx context.Context, lat float64, lng float64) {
	defer close(w.out)

	q := url.Values{}
	q.Set("action", "query")
	q.Set("list", "geosearch")
	q.Set("gscoord", fmt.Sprintf("%.6f|%.6f", lat, lng))
	q.Set("gsradius", "1000")
	q.Set("gslimit", "10")
	q.Set("format", "json")
	q.Set("utf8", "1")

	u := baseURL + q.Encode()

	geoCh := make(chan result.Result[wikiGeoResp], 1)
	defer close(geoCh)
	go httpx.HttpWork[wikiGeoResp, wikiGeoResp](ctx, u, func(x wikiGeoResp) wikiGeoResp { return x }, geoCh)

	geoRes := <-geoCh
	if geoRes.Err != nil {
		w.out <- result.Result[[]Location]{Err: geoRes.Err}
		return
	}
	ids, base := toIDs(geoRes.Val)
	if len(ids) == 0 {
		w.out <- result.Result[[]Location]{Val: []Location{}}
		return
	}

	dq := url.Values{}
	dq.Set("action", "query")
	dq.Set("prop", "extracts|info")
	dq.Set("exintro", "1")
	dq.Set("explaintext", "1")
	dq.Set("inprop", "url")
	dq.Set("pageids", strings.Join(ids, "|")) // список id через |
	dq.Set("format", "json")
	dq.Set("utf8", "1")

	detailsURL := baseURL + dq.Encode()

	detailsCh := make(chan result.Result[wikiDetailsResp], 1)
	defer close(detailsCh)
	go httpx.HttpWork[wikiDetailsResp, wikiDetailsResp](ctx, detailsURL, func(x wikiDetailsResp) wikiDetailsResp { return x }, detailsCh)

	detailsRes := <-detailsCh
	if detailsRes.Err != nil {
		w.out <- result.Result[[]Location]{Err: detailsRes.Err}
		return
	}

	final := mergeDetails(base, detailsRes.Val)
	w.out <- result.Result[[]Location]{Val: final}
}

func toIDs(r wikiGeoResp) ([]string, []Location) {
	gs := r.Query.Geosearch
	ids := make([]string, 0, len(gs))
	base := make([]Location, 0, len(gs))
	for _, it := range gs {
		if it.Title == "" {
			continue
		}
		ids = append(ids, strconv.Itoa(it.PageID))
		base = append(base, Location{
			ID:   strconv.Itoa(it.PageID),
			Name: it.Title,
		})
	}
	return ids, base
}

func mergeDetails(base []Location, d wikiDetailsResp) []Location {
	byID := make(map[string]*Location, len(base))
	for i := range base {
		byID[base[i].ID] = &base[i]
	}
	for _, p := range d.Query.Pages {
		id := strconv.Itoa(p.PageID)
		if dst, ok := byID[id]; ok {
			dst.Summary = p.Extract
			dst.URL = p.FullURL
		}
	}

	out := make([]Location, 0, len(base))
	for _, v := range byID {
		out = append(out, *v)
	}
	return out
}
