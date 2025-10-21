package geocode

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/art9440/Network-labs/lab3/internal/domain/httpx"
	"github.com/art9440/Network-labs/lab3/internal/shared/result"
)

const baseURL = "https://graphhopper.com/api/1/geocode?"

type Place struct {
	Name        string
	Country     string
	City        string
	Street      string
	HouseNumber string
	Lat, Lng    float64
}

type GeoCode struct {
	out chan result.Result[[]Place]
}

type GhResp struct {
	Hits []struct {
		Name        string `json:"name"`
		Country     string `json:"country"`
		City        string `json:"city"`
		Street      string `json:"street"`
		HouseNumber string `json:"housenumber"`
		Point       struct {
			Lat float64 `json:"lat"`
			Lng float64 `json:"lng"`
		} `json:"point"`
	} `json:"hits"`
}

func NewGeoCode(out chan result.Result[[]Place]) *GeoCode {
	return &GeoCode{out: out}
}

func (g GeoCode) HttpGeocode(ctx context.Context, query string) {
	defer close(g.out)
	u := fmt.Sprintf(baseURL+"key=%s&q=%s&locale=ru&limit=5", os.Getenv("GEOCODE_API_KEY"), url.QueryEscape(query))

	httpx.HttpWork[GhResp, []Place](ctx, u, toPlaces, g.out)

}

func toPlaces(r GhResp) []Place {
	places := make([]Place, 0, len(r.Hits))

	for _, h := range r.Hits {
		places = append(places, Place{
			Name:        h.Name,
			Country:     h.Country,
			City:        h.City,
			Street:      h.Street,
			HouseNumber: h.HouseNumber,
			Lat:         h.Point.Lat,
			Lng:         h.Point.Lng,
		})
	}

	return places
}
