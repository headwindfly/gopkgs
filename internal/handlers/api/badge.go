package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"clevergo.tech/clevergo"
	"clevergo.tech/shields"
	"github.com/razonyang/gopkgs/internal/models"
)

func (h *Handler) download(c *clevergo.Context) error {
	interval := c.Params.String("interval")
	toDate := time.Now()
	var fromDate time.Time
	switch interval {
	case "day":
		fromDate = toDate.AddDate(0, 0, -1)
	case "week":
		fromDate = toDate.AddDate(0, 0, -7)
	case "month":
		fromDate = toDate.AddDate(0, 0, -30)
	default:
		return fmt.Errorf("invalid interval parameter")
	}

	path := strings.Split(strings.TrimPrefix(c.Params.String("path"), "/"), "/")
	if len(path) < 2 {
		return c.NotFound()
	}
	ctx := c.Context()
	var pkg models.Package
	err := models.FindPackageByDomainAndPath(ctx, h.DB, &pkg, path[0], strings.Join(path[1:], "/"))
	if err != nil {
		if err == sql.ErrNoRows {
			return c.NotFound()
		}
		return err
	}

	query := `
SELECT COUNT(1) FROM actions
WHERE package_id = ? 
	AND created_at BETWEEN ? AND ?
`
	var count int64
	if err := h.DB.GetContext(ctx, &count, query, pkg.ID, fromDate.Format("2006-01-02"), toDate.Format("2006-01-02")); err != nil {
		return err
	}

	badge := shields.New("downloads", fmt.Sprintf("%d/%s", count, interval))
	badge.LabelColor = shields.ColorGreen
	if err := badge.ParseRequest(c.Request); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, badge)
}