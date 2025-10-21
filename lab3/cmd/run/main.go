package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/art9440/Network-labs/lab3/internal/domain/geocode"
	"github.com/art9440/Network-labs/lab3/internal/domain/openweather"
	"github.com/art9440/Network-labs/lab3/internal/domain/wiki"
	"github.com/art9440/Network-labs/lab3/internal/shared/result"
	"github.com/joho/godotenv"
)

func init() { _ = godotenv.Load() }

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	geoCodeOut := StartGeocoding(ctx)

	res := <-geoCodeOut
	if res.Err != nil {
		fmt.Println("Geocoding failed:", res.Err)
		return
	}
	//вывод geocode
	places := res.Val
	for i, p := range places {
		fmt.Printf("%d) %s — %.6f, %.6f (%s, %s, %s %s)\n",
			i+1, p.Name, p.Lat, p.Lng, p.Country, p.City, p.Street, p.HouseNumber)
	}

	fmt.Println("Choose the location by writing number")
	var number string
	var num int
	var err error
	for {
		fmt.Scan(&number)
		num, err = strconv.Atoi(number)
		if err != nil {
			fmt.Println("Something ewent wrong while converting: ", err)
		}
		if num > len(places) || num <= 0 {
			continue
		} else {
			break
		}
	}
	searchFeatures(places[num-1], ctx)
}

func StartGeocoding(ctx context.Context) <-chan result.Result[[]geocode.Place] {

	//geocode
	out := make(chan result.Result[[]geocode.Place], 1)
	g := geocode.NewGeoCode(out)
	var location string
	fmt.Printf("Write a location\n")
	fmt.Scan(&location)
	g.HttpGeocode(ctx, location)

	return out
}

func searchFeatures(p geocode.Place, ctx context.Context) {
	openWeatherOut := make(chan result.Result[openweather.Weather], 1)
	wikiOut := make(chan result.Result[[]wiki.Location], 1)

	openWeather := openweather.NewOpenWeather(openWeatherOut)
	wiki := wiki.NewWikiLocation(wikiOut)

	lat, long := p.Lat, p.Lng
	go openWeather.HttpOpenWeather(ctx, lat, long)
	go wiki.HttpWikiLocation(ctx, lat, long)

	resOpenWeather := <-openWeatherOut
	resWiki := <-wikiOut

	if resOpenWeather.Err != nil {
		fmt.Println("weather error:", resOpenWeather.Err)
	} else {
		w := resOpenWeather.Val
		cond := "—"
		if len(w.Conditions) > 0 {
			cond = w.Conditions[0].Description
		}
		fmt.Printf("Погода: %.1f°C, условия: %s\n", w.Temp, cond)
	}

	// --- Вывод локаций ---
	if resWiki.Err != nil {
		fmt.Println("wikipedia error:", resWiki.Err)
	} else {
		locs := resWiki.Val
		if len(locs) == 0 {
			fmt.Println("Локации не найдены поблизости.")
			return
		}
		fmt.Printf("\nНайдено %d мест рядом с %.6f, %.6f:\n", len(locs), lat, long)
		for i, loc := range locs {
			fmt.Printf("%2d) %s (pageid=%s)\n", i+1, loc.Name, loc.ID)
			if loc.Summary != "" {
				// короткий обрез — чтобы не заливать весь экран
				const maxLen = 220
				sum := loc.Summary
				if len([]rune(sum)) > maxLen {
					runes := []rune(sum)
					sum = string(runes[:maxLen]) + "…"
				}
				fmt.Printf("    %s\n", sum)
			}
			if loc.URL != "" {
				fmt.Printf("    %s\n", loc.URL)
			}
		}
	}
}
