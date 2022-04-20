package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/samber/lo"
)

type TimeEntry struct {
	ID          int64     `json:"id"`
	GUID        string    `json:"guid"`
	Wid         int       `json:"wid"`
	Pid         int       `json:"pid"`
	Billable    bool      `json:"billable"`
	Start       time.Time `json:"start"`
	Stop        time.Time `json:"stop,omitempty"`
	Duration    int       `json:"duration"`
	Description string    `json:"description,omitempty"`
	Duronly     bool      `json:"duronly"`
	At          time.Time `json:"at"`
	UID         int       `json:"uid"`
}

type PatchEntry struct {
	Start time.Time `json:"start"`
	Stop  time.Time `json:"stop"`
}

func (entry TimeEntry) String() string {
	return fmt.Sprintf("%s (%s -> %s)", entry.Description, entry.Start, entry.Stop)
}

func normalize(start, stop time.Time) (time.Time, time.Time) {
	start = start.Round(time.Minute)
	stop = start.Add(stop.Sub(start).Round(5 * time.Minute))

	return start, stop
}

func run() error {
	c := resty.New().SetBasicAuth("183daa8dd295f03f924ffe44b92626a5", "api_token")

	var timeEntries []TimeEntry
	_, err := c.R().SetResult(&timeEntries).Get("https://api.track.toggl.com/api/v8/time_entries")
	if err != nil {
		return err
	}

	timeEntries = lo.Filter(timeEntries, func(entry TimeEntry, _ int) bool {
		start, stop := normalize(entry.Start, entry.Stop)
		if start.Equal(entry.Start) && stop.Equal(entry.Stop) {
			return false
		}

		return entry.Duration > 0 && entry.Description == "XX"
	})
	
	timeEntries = lo.Map(timeEntries, func(entry TimeEntry, _ int) TimeEntry {
		oldEntry := entry.String()
		entry.Start, entry.Stop = normalize(entry.Start, entry.Stop)
		newEntry := entry.String()

		fmt.Printf("%s -> %s\n", oldEntry, newEntry)

		return entry
	})

	for _, entry := range timeEntries {
		out, err := json.Marshal(struct {
			PatchEntry `json:"time_entry"`
		}{
			PatchEntry{
				Start: entry.Start,
				Stop:  entry.Stop,
			},
		})
		if err != nil {
			return err
		}

		_, err = c.R().SetBody(out).Put(fmt.Sprintf("https://api.track.toggl.com/api/v8/time_entries/%d", entry.ID))
		if err != nil {
			return err
		}

		fmt.Printf("Updated %d", entry.ID)
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
