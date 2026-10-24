// Command questschedule validates a delivery manifest and exports the same
// public calendar data as the server. It never connects to a database.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"erupe-ce/common/questdelivery"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("questschedule", flag.ContinueOnError)
	input := flags.String("input", "", "delivery manifest JSON")
	locations := flags.String("locations", "", "explicit configured enabled world/channel tuples for verified common export")
	listingIDs := flags.String("listing-ids", "", "optional exact-manifest listing-source inventory JSON; reject missing managed IDs")
	output := flags.String("output", "", "public schedule JSON (default stdout)")
	start := flags.String("from", "", "first calendar date in KST, YYYY-MM-DD (default today)")
	days := flags.Int("days", 30, "calendar days, 1..31")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" {
		return fmt.Errorf("-input is required; positional arguments are not supported")
	}
	if *days < 1 || *days > 31 {
		return fmt.Errorf("-days must be between 1 and 31")
	}
	info, err := os.Stat(*input)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return fmt.Errorf("input must be a regular file no larger than 16 MiB")
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	catalog, err := questdelivery.Parse(data)
	if err != nil {
		return err
	}
	if *listingIDs != "" {
		if err := checkListingInventory(*listingIDs, data, catalog); err != nil {
			return err
		}
	}
	zone := time.FixedZone("KST", 9*60*60)
	now := time.Now().In(zone)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	if *start != "" {
		from, err = time.ParseInLocation(time.DateOnly, *start, zone)
		if err != nil {
			return fmt.Errorf("-from must be YYYY-MM-DD: %w", err)
		}
	}
	// Raw exports retain their legacy envelope. Only explicit validated locations
	// can create the same common PublicDocument used by the HTTP endpoint.
	var document questdelivery.ScheduleDocument
	if *locations != "" {
		configured, readErr := readPublicationLocations(*locations)
		if readErr != nil {
			return readErr
		}
		cohort, prepareErr := questdelivery.BuildCommonPublicationCohort(catalog, configured)
		if prepareErr != nil {
			return prepareErr
		}
		document, err = cohort.PublicDocument(from, from.AddDate(0, 0, *days))
	} else {
		document, err = catalog.PublicDocument(from, from.AddDate(0, 0, *days))
	}
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if *output != "" {
		inputAbs, err := filepath.Abs(*input)
		if err != nil {
			return err
		}
		outputAbs, err := filepath.Abs(*output)
		if err != nil {
			return err
		}
		sameFile := filepath.Clean(inputAbs) == filepath.Clean(outputAbs)
		if outputInfo, statErr := os.Stat(*output); statErr == nil {
			sameFile = sameFile || os.SameFile(info, outputInfo)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if sameFile {
			return fmt.Errorf("input and output must be different files")
		}
		if *listingIDs != "" {
			listingAbs, err := filepath.Abs(*listingIDs)
			if err != nil {
				return err
			}
			sameInventory := filepath.Clean(listingAbs) == filepath.Clean(outputAbs)
			if outputInfo, statErr := os.Stat(*output); statErr == nil {
				inventoryInfo, err := os.Stat(*listingIDs)
				if err != nil {
					return err
				}
				sameInventory = sameInventory || os.SameFile(inventoryInfo, outputInfo)
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
			if sameInventory {
				return fmt.Errorf("listing inventory and output must be different files")
			}
		}
		if *locations != "" {
			locationAbs, err := filepath.Abs(*locations)
			if err != nil {
				return err
			}
			same := filepath.Clean(locationAbs) == filepath.Clean(outputAbs)
			if outInfo, statErr := os.Stat(*output); statErr == nil {
				sourceInfo, err := os.Stat(*locations)
				if err != nil {
					return err
				}
				same = same || os.SameFile(outInfo, sourceInfo)
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
			if same {
				return fmt.Errorf("locations and output must be different files")
			}
		}
		return writeScheduleFile(*output, encoded)
	}
	_, err = stdout.Write(encoded)
	return err
}

func readPublicationLocations(path string) ([]questdelivery.PublicationLocation, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, fmt.Errorf("locations must be a regular file no larger than16MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var locations []questdelivery.PublicationLocation
	if err := decoder.Decode(&locations); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("locations require exactly one JSON array")
	}
	return locations, nil
}
