package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"wacampaigns/internal/phone"
	"wacampaigns/internal/settings"
)

const maxImportRows = 50_000

type importIssue struct {
	Row    int    `json:"row"`
	Phone  string `json:"phone"`
	Name   string `json:"name"`
	Status string `json:"status"` // invalid | duplicate
	Reason string `json:"reason"`
}

type importResult struct {
	ID         int64             `json:"id"`
	Filename   string            `json:"filename"`
	Total      int               `json:"total"`
	Created    int               `json:"created"`
	Updated    int               `json:"updated"`
	Duplicates int               `json:"duplicates"`
	Invalid    int               `json:"invalid"`
	Columns    map[string]string `json:"columns"`
	Issues     []importIssue     `json:"issues"`
	CreatedAt  time.Time         `json:"created_at"`
}

// readTable returns all rows of a CSV or the first sheet of an Excel file.
func readTable(filename string, data []byte) ([][]string, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".xlsx", ".xlsm", ".xls":
		if strings.ToLower(filepath.Ext(filename)) == ".xls" {
			return nil, errors.New("old .xls files are not supported; save as .xlsx or .csv")
		}
		f, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("cannot read Excel file: %w", err)
		}
		defer f.Close()
		sheets := f.GetSheetList()
		if len(sheets) == 0 {
			return nil, errors.New("the Excel file has no sheets")
		}
		return f.GetRows(sheets[0], excelize.Options{RawCellValue: true})
	case ".csv", ".txt":
		data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
		if !utf8.Valid(data) {
			return nil, errors.New("the CSV file is not UTF-8; in Excel use 'Save As > CSV UTF-8'")
		}
		r := csv.NewReader(bytes.NewReader(data))
		r.Comma = sniffDelimiter(data)
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		r.TrimLeadingSpace = true
		var rows [][]string
		for {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("CSV line %d: %w", len(rows)+1, err)
			}
			rows = append(rows, rec)
		}
		return rows, nil
	}
	return nil, errors.New("upload a .csv or .xlsx file")
}

func sniffDelimiter(data []byte) rune {
	line := data
	if i := bytes.IndexByte(data, '\n'); i > 0 {
		line = data[:i]
	}
	best, bestN := ',', 0
	for _, d := range []rune{',', ';', '\t', '|'} {
		if n := strings.Count(string(line), string(d)); n > bestN {
			best, bestN = d, n
		}
	}
	return best
}

var headerAliases = map[string][]string{
	"phone":      {"phone", "mobile", "number", "whatsapp", "tel", "telephone", "cell", "msisdn", "phone number", "mobile number", "رقم", "الجوال", "الهاتف", "رقم الجوال", "رقم الهاتف"},
	"name":       {"name", "full name", "fullname", "customer", "customer name", "contact", "contact name", "الاسم"},
	"first_name": {"first name", "firstname", "first", "given name"},
	"last_name":  {"last name", "lastname", "last", "surname", "family name"},
	"email":      {"email", "e-mail", "email address", "mail"},
	"tags":       {"tags", "tag", "labels", "label", "segment"},
}

// detectColumns maps known fields to column indexes. hasHeader is false when
// the first row already contains data.
func detectColumns(first []string) (map[string]int, bool) {
	cols := map[string]int{}
	for i, h := range first {
		h = strings.ToLower(strings.TrimSpace(h))
		for field, aliases := range headerAliases {
			if _, taken := cols[field]; taken {
				continue
			}
			for _, a := range aliases {
				if h == a {
					cols[field] = i
				}
			}
		}
	}
	if _, ok := cols["phone"]; ok {
		return cols, true
	}
	// No recognised header: assume "phone, name" columns with no header row.
	return map[string]int{"phone": 0, "name": 1}, false
}

func cell(row []string, cols map[string]int, field string) string {
	i, ok := cols[field]
	if !ok || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func (s *Server) importContacts(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "choose a file to upload")
		return
	}
	if fh.Size > 10<<20 {
		fail(c, 400, "file is larger than 10 MB")
		return
	}
	f, err := fh.Open()
	if err != nil {
		serverError(c, err)
		return
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		serverError(c, err)
		return
	}
	rows, err := readTable(fh.Filename, data)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	if len(rows) == 0 {
		fail(c, 400, "the file is empty")
		return
	}
	cols, hasHeader := detectColumns(rows[0])
	start := 0
	if hasHeader {
		start = 1
	}
	if len(rows)-start > maxImportRows {
		fail(c, 400, fmt.Sprintf("the file has more than %d rows; split it into smaller files", maxImportRows))
		return
	}

	updateExisting := c.PostForm("update_existing") != "false"
	extraTags := cleanTags(strings.Split(c.PostForm("tags"), ","))
	var groupID *int64
	if g, _ := strconv.ParseInt(c.PostForm("group_id"), 10, 64); g > 0 {
		groupID = &g
	}
	st, _ := settings.Load(c, s.db)

	tx, err := s.db.Begin(c)
	if err != nil {
		serverError(c, err)
		return
	}
	defer tx.Rollback(c)

	if name := strings.TrimSpace(c.PostForm("new_group_name")); name != "" && groupID == nil {
		var g int64
		err := tx.QueryRow(c, `INSERT INTO groups (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name RETURNING id`, name).Scan(&g)
		if err != nil {
			serverError(c, err)
			return
		}
		groupID = &g
	}

	res := importResult{Filename: fh.Filename, Issues: []importIssue{}, Columns: map[string]string{}}
	if hasHeader {
		for field, i := range cols {
			res.Columns[field] = rows[0][i]
		}
	}
	type valid struct {
		row         int
		phone, name string
		email       string
		tags        []string
	}
	var good []valid
	seen := map[string]int{}
	for i := start; i < len(rows); i++ {
		row := rows[i]
		rowNum := i + 1
		raw := cell(row, cols, "phone")
		name := cell(row, cols, "name")
		if name == "" {
			name = strings.TrimSpace(cell(row, cols, "first_name") + " " + cell(row, cols, "last_name"))
		}
		if raw == "" && name == "" && strings.TrimSpace(strings.Join(row, "")) == "" {
			continue // blank line
		}
		res.Total++
		p, err := phone.Normalize(raw, st.DefaultRegion)
		if err != nil {
			res.Invalid++
			res.Issues = append(res.Issues, importIssue{Row: rowNum, Phone: raw, Name: name, Status: "invalid", Reason: err.Error()})
			continue
		}
		if first, dup := seen[p]; dup {
			res.Duplicates++
			res.Issues = append(res.Issues, importIssue{Row: rowNum, Phone: raw, Name: name, Status: "duplicate",
				Reason: fmt.Sprintf("same number as row %d (%s)", first, p)})
			continue
		}
		seen[p] = rowNum
		tags := append(cleanTags(strings.FieldsFunc(cell(row, cols, "tags"), func(r rune) bool { return r == ';' || r == ',' || r == '|' })), extraTags...)
		good = append(good, valid{row: rowNum, phone: p, name: name, email: cell(row, cols, "email"), tags: cleanTags(tags)})
	}

	// Upsert in batches. Existing contacts keep their opt-out status; names are
	// only overwritten when the file has one and update_existing is on.
	const chunk = 500
	for i := 0; i < len(good); i += chunk {
		end := min(i+chunk, len(good))
		batch := &pgx.Batch{}
		for _, v := range good[i:end] {
			batch.Queue(`INSERT INTO contacts (phone, name, email, tags) VALUES ($1,$2,$3,$4)
				ON CONFLICT (phone) DO UPDATE SET
				  name  = CASE WHEN $5 AND EXCLUDED.name <> '' THEN EXCLUDED.name ELSE contacts.name END,
				  email = CASE WHEN $5 AND EXCLUDED.email <> '' THEN EXCLUDED.email ELSE contacts.email END,
				  tags  = ARRAY(SELECT DISTINCT unnest(contacts.tags || EXCLUDED.tags)),
				  updated_at = now()
				RETURNING id, (xmax = 0)`, v.phone, v.name, v.email, v.tags, updateExisting)
		}
		br := tx.SendBatch(c, batch)
		ids := make([]int64, 0, end-i)
		for range good[i:end] {
			var id int64
			var inserted bool
			if err := br.QueryRow().Scan(&id, &inserted); err != nil {
				br.Close()
				serverError(c, err)
				return
			}
			ids = append(ids, id)
			if inserted {
				res.Created++
			} else {
				res.Updated++
			}
		}
		if err := br.Close(); err != nil {
			serverError(c, err)
			return
		}
		if groupID != nil {
			if _, err := tx.Exec(c, `INSERT INTO contact_groups (contact_id, group_id) SELECT unnest($1::bigint[]), $2 ON CONFLICT DO NOTHING`, ids, *groupID); err != nil {
				serverError(c, err)
				return
			}
		}
	}

	report, _ := json.Marshal(res.Issues)
	uid := user(c).ID
	err = tx.QueryRow(c, `INSERT INTO import_batches (filename, user_id, total, created, updated, duplicates, invalid, report)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, created_at`,
		fh.Filename, uid, res.Total, res.Created, res.Updated, res.Duplicates, res.Invalid, report).Scan(&res.ID, &res.CreatedAt)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := tx.Commit(c); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, res)
}

func (s *Server) listImports(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT id, filename, total, created, updated, duplicates, invalid, created_at
		FROM import_batches ORDER BY id DESC LIMIT 50`)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	out := []importResult{}
	for rows.Next() {
		var r importResult
		if err := rows.Scan(&r.ID, &r.Filename, &r.Total, &r.Created, &r.Updated, &r.Duplicates, &r.Invalid, &r.CreatedAt); err != nil {
			serverError(c, err)
			return
		}
		out = append(out, r)
	}
	c.JSON(200, out)
}

func (s *Server) getImport(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var r importResult
	var report []byte
	err := s.db.QueryRow(c, `SELECT id, filename, total, created, updated, duplicates, invalid, report, created_at
		FROM import_batches WHERE id=$1`, id).Scan(&r.ID, &r.Filename, &r.Total, &r.Created, &r.Updated, &r.Duplicates, &r.Invalid, &report, &r.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			fail(c, 404, "import not found")
			return
		}
		serverError(c, err)
		return
	}
	_ = json.Unmarshal(report, &r.Issues)
	c.JSON(200, r)
}
