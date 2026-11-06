// Package chatops updates the master data in the db from a Google Spreadsheet on a Discord
// slash command.
package chatops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SheetReader returns the cells of every sheet of a spreadsheet as text, rows of cells.
type SheetReader interface {
	ReadAll(ctx context.Context) ([][][]string, error)
}

// Spreadsheet reads a Google Spreadsheet through the Sheets API v4.
type Spreadsheet struct {
	ID string
	// Client authorizes the requests (scope spreadsheets.readonly).
	Client *http.Client
	// BaseURL replaces https://sheets.googleapis.com, for tests.
	BaseURL string
}

const sheetRange = "A1:Z1000"

func (s *Spreadsheet) ReadAll(ctx context.Context) ([][][]string, error) {
	base := s.BaseURL
	if base == "" {
		base = "https://sheets.googleapis.com"
	}
	base += "/v4/spreadsheets/" + url.PathEscape(s.ID)

	var meta struct {
		Sheets []struct {
			Properties struct {
				Title string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := s.get(ctx, base+"?fields=sheets.properties.title", &meta); err != nil {
		return nil, err
	}
	if len(meta.Sheets) == 0 {
		return nil, nil
	}

	q := url.Values{}
	for _, sh := range meta.Sheets {
		q.Add("ranges", "'"+strings.ReplaceAll(sh.Properties.Title, "'", "''")+"'!"+sheetRange)
	}
	var values struct {
		ValueRanges []struct {
			Values [][]string `json:"values"`
		} `json:"valueRanges"`
	}
	if err := s.get(ctx, base+"/values:batchGet?"+q.Encode(), &values); err != nil {
		return nil, err
	}
	ret := make([][][]string, 0, len(values.ValueRanges))
	for _, vr := range values.ValueRanges {
		ret = append(ret, vr.Values)
	}
	return ret, nil
}

func (s *Spreadsheet) get(ctx context.Context, u string, v interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("sheets api: %s: %.200s", res.Status, body)
	}
	return json.Unmarshal(body, v)
}

// ParseTables finds the tables in the sheets. A table starts at a cell "@<name>" (name starting
// with "m_"), its header row is the row below from that column up to the first empty cell, and
// its data rows follow up to the first row empty in those columns. The header row is the first
// row of each table; short rows are padded with "".
func ParseTables(sheets [][][]string) (map[string][][]string, error) {
	tables := map[string][][]string{}
	for _, rows := range sheets {
		for i := 0; i < len(rows); i++ {
			left := -1
			for j, c := range rows[i] {
				if strings.HasPrefix(c, "@m_") {
					left = j
				}
			}
			if left < 0 || i+1 >= len(rows) {
				continue
			}
			name := strings.TrimSpace(rows[i][left][1:])
			if _, ok := tables[name]; ok {
				return nil, fmt.Errorf("table %s appears twice", name)
			}
			right := left
			for right < len(rows[i+1]) && rows[i+1][right] != "" {
				right++
			}
			if right == left {
				return nil, fmt.Errorf("table %s has no header", name)
			}
			var table [][]string
			for i = i + 1; i < len(rows); i++ {
				r := make([]string, right-left)
				empty := true
				for j := left; j < right && j < len(rows[i]); j++ {
					r[j-left] = strings.TrimSpace(rows[i][j])
					if r[j-left] != "" {
						empty = false
					}
				}
				if empty {
					break
				}
				table = append(table, r)
			}
			tables[name] = table
		}
	}
	return tables, nil
}
