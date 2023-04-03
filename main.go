package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/olekukonko/tablewriter"
	"github.com/samber/lo"
	"github.com/urfave/cli/v2"
)

type TimeEntry struct {
	ID          int64     `json:"id"`
	GUID        string    `json:"guid"`
	Wid         int       `json:"wid"`
	Pid         int       `json:"pid"`
	Billable    bool      `json:"billable"`
	Start       time.Time `json:"start"`
	Stop        time.Time `json:"stop,omitempty"`
	Duration    int       `json:"rounding"`
	Description string    `json:"description,omitempty"`
	Duronly     bool      `json:"duronly"`
	At          time.Time `json:"at"`
	UID         int       `json:"uid"`
}

type PatchEntry struct {
	Duration int `json:"duration"`
}

func (entry TimeEntry) String() string {
	return fmt.Sprintf("%s (%s -> %s)", entry.Description, entry.Start, entry.Stop)
}

func roundUp(t time.Duration, m time.Duration) time.Duration {
	n := t.Round(m)

	if n-t < 0 {
		n += m
	}

	return n
}

func roundEntries(client *resty.Client, apply bool, rounding, timeframe time.Duration) error {
	timeEntries, err := fetchTimeEntries(client, timeframe)
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Description", "Start Time", "Old Duration", "New Duration"})

	lo.ForEach(timeEntries, func(entry TimeEntry, _ int) {
		oldDuration := entry.Stop.Sub(entry.Start)
		newDuration := roundUp(oldDuration, rounding)
		if oldDuration == 0 || oldDuration == newDuration {
			return
		}

		// fmt.Printf("changing %d (%s) from %s to %s\n", entry.ID, entry.Description, oldDuration, newDuration)

		row := []string{
			strconv.FormatInt(entry.ID, 10),
			entry.Description,
			entry.Start.Format(time.RFC822),
			oldDuration.String(),
		}
		if apply {
			out, err := json.Marshal(struct {
				PatchEntry `json:"time_entry"`
			}{
				PatchEntry{
					Duration: int(newDuration / time.Second),
				},
			})
			if err != nil {
				log.Fatal(err) // TODO change to return
			}

			_, err = client.R().SetBody(out).Put(fmt.Sprintf("https://api.track.toggl.com/api/v8/time_entries/%d", entry.ID))
			if err != nil {
				log.Fatal(err) // TODO change to return
			}

			row = append(row, newDuration.String())
		} else {
			row = append(row, "-")
		}

		table.Append(row)
	})

	table.Render()

	return nil
}

func fetchTimeEntries(client *resty.Client, timeframe time.Duration) ([]TimeEntry, error) {
	var timeEntries []TimeEntry
	startTime := time.Now().Add(-timeframe)
	params := url.Values{}
	params.Add("start_date", startTime.Format(time.RFC3339))

	_, err := client.R().SetResult(&timeEntries).Get("https://api.track.toggl.com/api/v8/time_entries?" + params.Encode())
	if err != nil {
		return nil, err
	}
	return timeEntries, nil
}

type personioTime struct {
	Start    time.Time
	Duration time.Duration
}

func personio(client *resty.Client, timeframe time.Duration, lunchBreak time.Duration, loc *time.Location) error {
	timeEntries, err := fetchTimeEntries(client, timeframe)
	if err != nil {
		return err
	}

	buckets := map[string][]TimeEntry{}
	for _, entry := range timeEntries {
		key := entry.Start.Format("2006-01-02")
		buckets[key] = append(buckets[key], entry)
	}

	var dayAggregations []personioTime
	for _, bucket := range buckets {
		dayAggregation := personioTime{}

		for _, b := range bucket {
			start := b.Start.In(loc)
			stop := b.Stop.In(loc)

			dayAggregation.Duration = dayAggregation.Duration + stop.Sub(start)
			if (dayAggregation.Start == time.Time{} || start.Before(dayAggregation.Start)) {
				dayAggregation.Start = start
			}
		}

		dayAggregations = append(dayAggregations, dayAggregation)
	}

	sort.Slice(dayAggregations, func(i, j int) bool {
		return dayAggregations[i].Start.Before(dayAggregations[j].Start)
	})

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Day", "Start Time", "End Time (1h Lunch)", "Duration"})

	for _, entry := range dayAggregations {
		table.Append([]string{
			entry.Start.Format("Mon 02. Jan 2006"),
			entry.Start.Format("15:04"),
			entry.Start.Add(entry.Duration).Add(lunchBreak).Format("15:04"),
			entry.Duration.String()},
		)
	}

	table.Render()

	return nil
}

func main() {
	var (
		apiToken   string
		apply      bool
		rounding   time.Duration
		timeframe  time.Duration
		lunchBreak time.Duration
		timezone   string
	)

	roundCommand := &cli.Command{
		Name:        "round",
		Description: "round your time entries in Toggl",
		Action: func(context *cli.Context) error {
			c := resty.New().SetBasicAuth(apiToken, "api_token")

			return roundEntries(c, apply, rounding, timeframe)
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "apply",
				Usage:       "Apply rounding changes",
				Value:       false,
				Destination: &apply,
			},
			&cli.DurationFlag{
				Name:        "rounding",
				Usage:       "Rounding rounding",
				Value:       5 * time.Minute,
				Destination: &rounding,
			},
			&cli.DurationFlag{
				Name:        "timeframe",
				Usage:       "Time frame before now",
				Value:       24 * 30 * time.Hour,
				Destination: &timeframe,
			},
		},
	}

	personioCommand := &cli.Command{
		Name:        "personio",
		Description: "Export your data for personion",
		Action: func(context *cli.Context) error {
			c := resty.New().SetBasicAuth(apiToken, "api_token")
			loc, err := time.LoadLocation(timezone)
			if err != nil {
				return err
			}

			return personio(c, timeframe, lunchBreak, loc)
		},
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:        "timeframe",
				Usage:       "Time frame before now",
				Value:       24 * 30 * time.Hour,
				Destination: &timeframe,
			},
			&cli.DurationFlag{
				Name:        "lunchbreak",
				Usage:       "Time taken for lunch",
				Value:       1 * time.Hour,
				Destination: &lunchBreak,
			},
			&cli.StringFlag{
				Name:        "timezone",
				Usage:       "IANA Timezone (https://en.wikipedia.org/wiki/List_of_tz_database_time_zones)",
				Value:       "Europe/Zurich",
				Destination: &timezone,
			},
		},
	}

	app := &cli.App{
		Action: func(ctx *cli.Context) error {
			return cli.ShowAppHelp(ctx)
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "api_token",
				Usage:       "API Token for Toggl",
				EnvVars:     []string{"TOGGL_API_TOKEN"},
				Required:    true,
				Destination: &apiToken,
			},
		},
		Commands: []*cli.Command{
			roundCommand, personioCommand,
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
