package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func NewProviderClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
	}
}

func main() {
	wundergroundAPIKey := flag.String("wunderground.api.key", "0123456789abcdef", "wunderground.com API key")
	forecastIoAPIKey := flag.String("forecastio.api.key", "0123456789abcdef", "forecast.io API key")
	flag.Parse()

	mw := multiWeatherProvider{
		openWeatherMap{client: NewProviderClient()},
		weatherUnderground{client: NewProviderClient(), apiKey: *wundergroundAPIKey},
		forecastIo{apiKey: *forecastIoAPIKey, geoCode: &googleGeoCode{}, client: NewProviderClient()},
	}

	http.HandleFunc("/weather/", func(w http.ResponseWriter, r *http.Request) {
		begin := time.Now()
		city := strings.SplitN(r.URL.Path, "/", 3)[2]

		temp, err := mw.temperature(city)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"city": city,
			"temp": temp,
			"took": time.Since(begin).String(),
		})
	})

	http.ListenAndServe(":8080", nil)
}

type weatherProvider interface {
	temperature(city string) (float64, error) // in Kelvin, naturally
}

type multiWeatherProvider []weatherProvider

func (w multiWeatherProvider) temperature(city string) (float64, error) {
	// Make a channel for temperatures, and a channel for errors.
	// Each provider will push a value into only one.
	temps := make(chan float64, len(w))
	errs := make(chan error, len(w))

	// For each provider, spawn a goroutine with an anonymous function.
	// That function will invoke the temperature method, and forward the response.
	for _, provider := range w {
		go func(p weatherProvider) {
			k, err := p.temperature(city)
			if err != nil {
				errs <- err
				return
			}
			temps <- k
		}(provider)
	}

	sum := 0.0

	// Collect a temperature or an error from each provider.
	for i := 0; i < len(w); i++ {
		select {
		case temp := <-temps:
			sum += temp
		case err := <-errs:
			return 0, err
		}
	}

	// Return the average, same as before.
	return sum / float64(len(w)), nil
}

type openWeatherMap struct {
	client *http.Client
}

func (w openWeatherMap) temperature(city string) (float64, error) {
	resp, err := w.client.Get("http://api.openweathermap.org/data/2.5/weather?q=" + city)
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	var d struct {
		Main struct {
			Kelvin float64 `json:"temp"`
		} `json:"main"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return 0, err
	}

	log.Printf("openWeatherMap: %s: %.2f", city, d.Main.Kelvin)
	return d.Main.Kelvin, nil
}

type weatherUnderground struct {
	apiKey string
	client *http.Client
}

func (w weatherUnderground) temperature(city string) (float64, error) {
	resp, err := w.client.Get("http://api.wunderground.com/api/" + w.apiKey + "/conditions/q/" + city + ".json")
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	var d struct {
		Observation struct {
			Celsius float64 `json:"temp_c"`
		} `json:"current_observation"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return 0, err
	}

	kelvin := d.Observation.Celsius + 273.15
	log.Printf("weatherUnderground: %s: %.2f", city, kelvin)
	return kelvin, nil
}

type forecastIo struct {
	apiKey string
	geoCode
	client *http.Client
}

func NewForecastIo(apiKey string, gc geoCode, c *http.Client) *forecastIo {
	return &forecastIo{apiKey: apiKey, geoCode: gc, client: c}
}

func (f forecastIo) temperature(city string) (float64, error) {

	l, err := f.geoCode.findCityLocation(city)
	if err != nil {
		return 0, err
	}

	lookupUrl := "https://api.forecast.io/forecast/" + f.apiKey + "/" + strconv.FormatFloat(l.Lat, 'f', -1, 64) + "," + strconv.FormatFloat(l.Lng, 'f', -1, 64)

	resp, err := f.client.Get(lookupUrl)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var rawmap map[string]*json.RawMessage
	err = json.Unmarshal(b, &rawmap)
	if err != nil {
		return 0, err
	}

	var current map[string]*json.RawMessage
	err = json.Unmarshal(*rawmap["currently"], &current)
	if err != nil {
		return 0, err
	}

	var temp float64
	json.Unmarshal(*current["temperature"], &temp)
	tempInKelvin := ((temp - 32) / 1.8) + 273.15

	log.Printf("forecastIo: %s: %.2f", city, tempInKelvin)

	return tempInKelvin, nil

}

type location struct {
	Lat float64 `json: "lat"`
	Lng float64 `json: "lng"`
}

type geoCode interface {
	findCityLocation(city string) (location, error)
}

type googleGeoCode struct{}

func (g googleGeoCode) findCityLocation(city string) (location, error) {

	resp, err := http.Get("https://maps.googleapis.com/maps/api/geocode/json?address=" + city + "&components=country")
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return location{}, err
	}

	var rawmap map[string]*json.RawMessage
	err = json.Unmarshal(b, &rawmap)
	if err != nil {
		return location{}, err
	}

	var results []*json.RawMessage
	err = json.Unmarshal(*rawmap["results"], &results)
	if err != nil {
		return location{}, err
	}

	var result map[string]*json.RawMessage
	err = json.Unmarshal(*results[0], &result)
	if err != nil {
		return location{}, err
	}

	var geometry map[string]*json.RawMessage
	err = json.Unmarshal(*result["geometry"], &geometry)
	if err != nil {
		return location{}, err
	}

	var l location
	err = json.Unmarshal(*geometry["location"], &l)
	if err != nil {
		return location{}, err
	}

	return l, nil

}
