package openweather

import (
	"context"
	"net/url"
	"os"
	"strconv"

	"github.com/art9440/Network-labs/lab3/internal/domain/httpx"
	"github.com/art9440/Network-labs/lab3/internal/shared/result"
)

const baseURL = "https://api.openweathermap.org/data/2.5/weather?"

type OpenWeather struct {
	out chan result.Result[Weather]
}

func NewOpenWeather(out chan result.Result[Weather]) *OpenWeather {
	return &OpenWeather{out: out}
}

type openWeatherResp struct {
	Weather []owCondition `json:"weather"`
	Main    struct {
		Temp float64 `json:"temp"`
	} `json:"main"`
}

type owCondition struct {
	ID          int    `json:"id"`
	Main        string `json:"main"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type Weather struct {
	Temp       float64
	Conditions []Condition
}

type Condition struct {
	ID          int
	Main        string
	Description string
	Icon        string
}

func (o OpenWeather) HttpOpenWeather(ctx context.Context, lat float64, lng float64) {
	defer close(o.out)

	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(lng, 'f', 6, 64))
	q.Set("appid", os.Getenv("OPENWEATHER_API_KEY"))
	q.Set("units", "metric") // чтобы пришли °C
	q.Set("lang", "ru")

	u := baseURL + q.Encode()
	httpx.HttpWork[openWeatherResp, Weather](ctx, u, toWeather, o.out)

}

func toWeather(r openWeatherResp) Weather {
	cs := make([]Condition, 0, len(r.Weather))
	for _, c := range r.Weather {
		cs = append(cs, Condition{
			ID:          c.ID,
			Main:        c.Main,
			Description: c.Description,
			Icon:        c.Icon,
		})
	}
	return Weather{
		Temp:       r.Main.Temp,
		Conditions: cs,
	}
}
