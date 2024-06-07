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
	WorkspaceId int       `json:"workspace_id"`
}

type PatchEntry struct {
	Duration    int    `json:"duration"`
	CreatedWith string `json:"created_with"`
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
		if entry.Duration < 0 { // currently running entry
			return
		}

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
			out, err := json.Marshal(
				PatchEntry{
					Duration:    int(newDuration / time.Second),
					CreatedWith: "https://github.com/elwin/toggler",
				})
			if err != nil {
				log.Fatal(err) // TODO change to return
			}

			_, err = client.R().SetBody(out).Put(fmt.Sprintf("https://api.track.toggl.com/api/v9/workspaces/%d/time_entries/%d", entry.WorkspaceId, entry.ID))
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
	endTime := time.Now()
	startTime := endTime.Add(-timeframe)
	params := url.Values{}
	params.Add("start_date", startTime.Format(time.RFC3339))
	params.Add("end_date", endTime.Format(time.RFC3339))

	_, err := client.R().SetResult(&timeEntries).Get("https://api.track.toggl.com/api/v9/me/time_entries?" + params.Encode())
	if err != nil {
		return nil, err
	}
	return timeEntries, nil
}

func summary(client *resty.Client, timeframe time.Duration, lunchBreak time.Duration, loc *time.Location) error {
	timeEntries, err := fetchTimeEntries(client, timeframe)
	if err != nil {
		return err
	}

	dayAggregations := aggregate(timeEntries, "2006-01-02", loc)

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

type summaryTime struct {
	Start    time.Time
	Duration time.Duration
}

func aggregate(timeEntries []TimeEntry, layout string, loc *time.Location) []summaryTime {
	buckets := map[string][]TimeEntry{}
	for _, entry := range timeEntries {
		key := entry.Start.Format(layout)
		buckets[key] = append(buckets[key], entry)
	}

	var agg []summaryTime
	for _, bucket := range buckets {
		curAgg := summaryTime{}

		for _, entry := range bucket {
			start := entry.Start.In(loc)
			stop := entry.Stop.In(loc)

			curAgg.Duration = curAgg.Duration + stop.Sub(start)
			if (curAgg.Start == time.Time{} || start.Before(curAgg.Start)) {
				curAgg.Start = start
			}
		}

		agg = append(agg, curAgg)
	}

	sort.Slice(agg, func(i, j int) bool {
		return agg[i].Start.Before(agg[j].Start)
	})

	return agg
}

func monthlyAggregation(client *resty.Client, timeframe time.Duration, loc *time.Location) error {
	timeEntries, err := fetchTimeEntries(client, timeframe)
	if err != nil {
		return err
	}

	aggregation := aggregate(timeEntries, "2006-01", loc)

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Month", "Duration"})

	for _, entry := range aggregation {
		table.Append([]string{
			entry.Start.Format("Jan 2006"),
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

	summaryCommand := &cli.Command{
		Name:        "summary",
		Description: "Summary of working days",
		Action: func(context *cli.Context) error {
			c := resty.New().SetBasicAuth(apiToken, "api_token")
			loc, err := time.LoadLocation(timezone)
			if err != nil {
				return err
			}

			return summary(c, timeframe, lunchBreak, loc)
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

	aggregationCommand := &cli.Command{
		Name:        "aggregation",
		Description: "Aggregation",
		Action: func(context *cli.Context) error {
			c := resty.New().SetBasicAuth(apiToken, "api_token")
			loc, err := time.LoadLocation(timezone)
			if err != nil {
				return err
			}

			return monthlyAggregation(c, timeframe, loc)
		},
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:        "timeframe",
				Usage:       "Time frame before now",
				Value:       24 * 30 * time.Hour,
				Destination: &timeframe,
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
		EnableBashCompletion: true,
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
			roundCommand, summaryCommand, aggregationCommand,
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
