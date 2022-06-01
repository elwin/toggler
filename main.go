package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
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

func roundEntries(apiToken string, apply bool, rounding, timeframe time.Duration) error {
	c := resty.New().SetBasicAuth(apiToken, "api_token")

	var timeEntries []TimeEntry
	startTime := time.Now().Add(-timeframe)
	params := url.Values{}
	params.Add("start_date", startTime.Format(time.RFC3339))

	_, err := c.R().SetResult(&timeEntries).Get("https://api.track.toggl.com/api/v8/time_entries?" + params.Encode())
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

			_, err = c.R().SetBody(out).Put(fmt.Sprintf("https://api.track.toggl.com/api/v8/time_entries/%d", entry.ID))
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

func main() {
	var (
		apiToken  string
		apply     bool
		rounding  time.Duration
		timeframe time.Duration
	)

	app := &cli.App{
		Action: func(ctx *cli.Context) error {
			return cli.ShowAppHelp(ctx)
		},
		Commands: []*cli.Command{
			{
				Name:        "round",
				Description: "round your time entries in Toggl",
				Action: func(context *cli.Context) error {
					return roundEntries(apiToken, apply, rounding, timeframe)
				},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "api_token",
						Usage:       "API Token for Toggl",
						EnvVars:     []string{"TOGGL_API_TOKEN"},
						Required:    true,
						Destination: &apiToken,
					},
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
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
