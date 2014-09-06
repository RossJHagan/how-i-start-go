package main

import (
	"testing"
)

type testFastWeatherProvider struct {
}

func (t testFastWeatherProvider) temperature(city string) (float64, error) {
	return 290, nil
}

type testSlowWeatherProvider struct {
}

func (t testSlowWeatherProvider) temperature(city string) (float64, error) {
	return 280, nil
}

func TestMultiTemperature(t *testing.T) {
	w := multiWeatherProvider{
		testSlowWeatherProvider{},
		testFastWeatherProvider{},
	}

	avgTemp, err := w.temperature("new york")
	if err != nil || 285 != avgTemp {
		t.Fail()
	}
}
