package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wacampaigns/internal/campaigns"
	"wacampaigns/internal/phone"
	"wacampaigns/internal/settings"
)

type groupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type contact struct {
	ID           int64      `json:"id"`
	Phone        string     `json:"phone"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	Tags         []string   `json:"tags"`
	OptedOut     bool       `json:"opted_out"`
	OptedOutAt   *time.Time `json:"opted_out_at"`
	OptOutSource string     `json:"opt_out_source"`
	Groups       []groupRef `json:"groups"`
	CreatedAt    time.Time  `json:"created_at"`
}

// contactFilter builds the WHERE clause shared by list, export and bulk.
func contactFilter(c *gin.Context) (string, []any) {
	where := []string{"true"}
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		add("(c.name ILIKE ? OR c.phone ILIKE ? OR c.email ILIKE ?)", "%"+q+"%")
	}
	if g, _ := strconv.ParseInt(c.Query("group_id"), 10, 64); g > 0 {
		add("EXISTS (SELECT 1 FROM contact_groups cg WHERE cg.contact_id=c.id AND cg.group_id=?)", g)
	}
	if t := strings.TrimSpace(c.Query("tag")); t != "" {
		add("? = ANY(c.tags)", t)
	}
	switch c.Query("status") {
	case "opted_out":
		where = append(where, "c.opted_out")
	case "subscribed":
		where = append(where, "NOT c.opted_out")
	}
	return strings.Join(where, " AND "), args
}

const contactCols = `c.id, c.phone, c.name, c.email, c.tags, c.opted_out, c.opted_out_at, c.opt_out_source, c.created_at,
	coalesce((SELECT json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY g.name)
	  FROM contact_groups cg JOIN groups g ON g.id=cg.group_id WHERE cg.contact_id=c.id), '[]')`

func scanContact(row interface{ Scan(...any) error }) (contact, error) {
	var ct contact
	var groups []byte
	err := row.Scan(&ct.ID, &ct.Phone, &ct.Name, &ct.Email, &ct.Tags, &ct.OptedOut, &ct.OptedOutAt, &ct.OptOutSource, &ct.CreatedAt, &groups)
	if err == nil {
		_ = json.Unmarshal(groups, &ct.Groups)
	}
	return ct, err
}

func (s *Server) listContacts(c *gin.Context) {
	where, args := contactFilter(c)
	page, size, offset := pageParams(c)
	var total int
	if err := s.db.QueryRow(c, `SELECT count(*) FROM contacts c WHERE `+where, args...).Scan(&total); err != nil {
		serverError(c, err)
		return
	}
	rows, err := s.db.Query(c, fmt.Sprintf(`SELECT %s FROM contacts c WHERE %s ORDER BY c.created_at DESC, c.id DESC LIMIT %d OFFSET %d`,
		contactCols, where, size, offset), args...)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	items := []contact{}
	for rows.Next() {
		ct, err := scanContact(rows)
		if err != nil {
			serverError(c, err)
			return
		}
		items = append(items, ct)
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

type contactInput struct {
	Phone    string   `json:"phone"`
	Name     string   `json:"name"`
	Email    string   `json:"email"`
	Tags     []string `json:"tags"`
	GroupIDs []int64  `json:"group_ids"`
}

func cleanTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] && len(t) <= 50 {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	return out
}

func (s *Server) saveContact(c *gin.Context, id int64) {
	var in contactInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "invalid request")
		return
	}
	st, _ := settings.Load(c, s.db)
	p, err := phone.Normalize(in.Phone, st.DefaultRegion)
	if err != nil {
		fail(c, 400, "Phone: "+err.Error())
		return
	}
	tx, err := s.db.Begin(c)
	if err != nil {
		serverError(c, err)
		return
	}
	defer tx.Rollback(c)
	if id == 0 {
		err = tx.QueryRow(c, `INSERT INTO contacts (phone, name, email, tags) VALUES ($1,$2,$3,$4) RETURNING id`,
			p, strings.TrimSpace(in.Name), strings.TrimSpace(in.Email), cleanTags(in.Tags)).Scan(&id)
	} else {
		_, err = tx.Exec(c, `UPDATE contacts SET phone=$2, name=$3, email=$4, tags=$5, updated_at=now() WHERE id=$1`,
			id, p, strings.TrimSpace(in.Name), strings.TrimSpace(in.Email), cleanTags(in.Tags))
	}
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			fail(c, 409, "Another contact already has this phone number")
			return
		}
		serverError(c, err)
		return
	}
	if in.GroupIDs != nil {
		if _, err := tx.Exec(c, `DELETE FROM contact_groups WHERE contact_id=$1`, id); err != nil {
			serverError(c, err)
			return
		}
		if _, err := tx.Exec(c, `INSERT INTO contact_groups (contact_id, group_id) SELECT $1, unnest($2::bigint[]) ON CONFLICT DO NOTHING`, id, in.GroupIDs); err != nil {
			serverError(c, err)
			return
		}
	}
	if err := tx.Commit(c); err != nil {
		serverError(c, err)
		return
	}
	ct, err := scanContact(s.db.QueryRow(c, `SELECT `+contactCols+` FROM contacts c WHERE c.id=$1`, id))
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, ct)
}

func (s *Server) createContact(c *gin.Context) { s.saveContact(c, 0) }

func (s *Server) updateContact(c *gin.Context) {
	if id, ok := idParam(c); ok {
		s.saveContact(c, id)
	}
}

func (s *Server) deleteContact(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM contacts WHERE id=$1`, id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) optOutContact(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var p string
	if err := s.db.QueryRow(c, `SELECT phone FROM contacts WHERE id=$1`, id).Scan(&p); err != nil {
		fail(c, 404, "contact not found")
		return
	}
	if _, err := campaigns.OptOut(c, s.db, p, "manual"); err != nil {
		serverError(c, err)
		return
	}
	if s.bird.Configured() {
		_ = s.bird.RecordPreference(c, p, "revoked", "Marked opted out in campaign platform")
	}
	c.JSON(200, gin.H{"ok": true})
}

// optInContact re-subscribes a contact. Admin only: it should be used only
// when the customer has asked to receive messages again.
func (s *Server) optInContact(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var p string
	if err := s.db.QueryRow(c, `SELECT phone FROM contacts WHERE id=$1`, id).Scan(&p); err != nil {
		fail(c, 404, "contact not found")
		return
	}
	if s.bird.Configured() {
		if err := s.bird.RecordPreference(c, p, "granted", "Customer re-consented (recorded by "+user(c).Email+")"); err != nil {
			fail(c, 502, "Bird did not accept the re-subscription: "+err.Error())
			return
		}
	}
	if _, err := s.db.Exec(c, `UPDATE contacts SET opted_out=false, opted_out_at=NULL, opt_out_source='', updated_at=now() WHERE id=$1`, id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) syncOptOuts(c *gin.Context) {
	prefs, err := s.bird.ListRevokedPreferences(c)
	if err != nil {
		fail(c, 502, "Could not read opt-outs from Bird: "+err.Error())
		return
	}
	n := 0
	for _, p := range prefs {
		if p.Handle == "" {
			continue
		}
		if changed, err := campaigns.OptOut(c, s.db, p.Handle, "bird"); err == nil && changed {
			n++
		}
	}
	c.JSON(200, gin.H{"checked": len(prefs), "newly_opted_out": n})
}

func (s *Server) bulkContacts(c *gin.Context) {
	var in struct {
		IDs     []int64 `json:"ids"`
		Action  string  `json:"action"` // delete | add_group | remove_group | add_tag | remove_tag
		GroupID int64   `json:"group_id"`
		Tag     string  `json:"tag"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || len(in.IDs) == 0 {
		fail(c, 400, "select at least one contact")
		return
	}
	tag := strings.TrimSpace(in.Tag)
	var err error
	switch in.Action {
	case "delete":
		_, err = s.db.Exec(c, `DELETE FROM contacts WHERE id = ANY($1)`, in.IDs)
	case "add_group":
		_, err = s.db.Exec(c, `INSERT INTO contact_groups (contact_id, group_id) SELECT unnest($1::bigint[]), $2 ON CONFLICT DO NOTHING`, in.IDs, in.GroupID)
	case "remove_group":
		_, err = s.db.Exec(c, `DELETE FROM contact_groups WHERE contact_id = ANY($1) AND group_id=$2`, in.IDs, in.GroupID)
	case "add_tag":
		if tag == "" {
			fail(c, 400, "tag is required")
			return
		}
		_, err = s.db.Exec(c, `UPDATE contacts SET tags = array_append(tags, $2), updated_at=now() WHERE id = ANY($1) AND NOT ($2 = ANY(tags))`, in.IDs, tag)
	case "remove_tag":
		_, err = s.db.Exec(c, `UPDATE contacts SET tags = array_remove(tags, $2), updated_at=now() WHERE id = ANY($1)`, in.IDs, tag)
	default:
		fail(c, 400, "unknown action")
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) listTags(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT t, count(*) FROM contacts, unnest(tags) t GROUP BY t ORDER BY t`)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	type tagCount struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	out := []tagCount{}
	for rows.Next() {
		var t tagCount
		if rows.Scan(&t.Tag, &t.Count) == nil {
			out = append(out, t)
		}
	}
	c.JSON(200, out)
}

func (s *Server) exportContacts(c *gin.Context) {
	where, args := contactFilter(c)
	rows, err := s.db.Query(c, `SELECT `+contactCols+` FROM contacts c WHERE `+where+` ORDER BY c.id`, args...)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="contacts-%s.csv"`, time.Now().In(Qatar).Format("2006-01-02")))
	c.Writer.Write([]byte("\xEF\xBB\xBF")) // BOM so Excel reads UTF-8 (Arabic names)
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"phone", "name", "email", "tags", "groups", "opted_out", "opted_out_at", "opt_out_source", "created_at"})
	for rows.Next() {
		ct, err := scanContact(rows)
		if err != nil {
			break
		}
		gn := make([]string, len(ct.Groups))
		for i, g := range ct.Groups {
			gn[i] = g.Name
		}
		_ = w.Write([]string{ct.Phone, csvSafe(ct.Name), csvSafe(ct.Email), strings.Join(ct.Tags, ";"), strings.Join(gn, ";"),
			strconv.FormatBool(ct.OptedOut), fmtTime(ct.OptedOutAt), ct.OptOutSource, ct.CreatedAt.In(Qatar).Format("2006-01-02 15:04")})
	}
	w.Flush()
}

// csvSafe prevents spreadsheet formula injection from user-supplied text.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.In(Qatar).Format("2006-01-02 15:04")
}

// ---- groups ----

func (s *Server) listGroups(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT g.id, g.name, g.description, g.created_at,
		count(cg.contact_id), count(cg.contact_id) FILTER (WHERE ct.opted_out)
		FROM groups g LEFT JOIN contact_groups cg ON cg.group_id=g.id LEFT JOIN contacts ct ON ct.id=cg.contact_id
		GROUP BY g.id ORDER BY g.name`)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	type group struct {
		ID          int64     `json:"id"`
		Name        string    `json:"name"`
		Description string    `json:"description"`
		CreatedAt   time.Time `json:"created_at"`
		Contacts    int       `json:"contacts"`
		OptedOut    int       `json:"opted_out"`
	}
	out := []group{}
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.Contacts, &g.OptedOut); err != nil {
			serverError(c, err)
			return
		}
		out = append(out, g)
	}
	c.JSON(200, out)
}

type groupInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) createGroup(c *gin.Context) {
	var in groupInput
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		fail(c, 400, "group name is required")
		return
	}
	var id int64
	err := s.db.QueryRow(c, `INSERT INTO groups (name, description) VALUES ($1,$2) RETURNING id`,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			fail(c, 409, "A group with that name already exists")
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) updateGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in groupInput
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		fail(c, 400, "group name is required")
		return
	}
	if _, err := s.db.Exec(c, `UPDATE groups SET name=$2, description=$3 WHERE id=$1`, id,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)); err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			fail(c, 409, "A group with that name already exists")
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) deleteGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM groups WHERE id=$1`, id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
