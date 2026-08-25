package httpkit

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Cursor is a keyset pagination position, encoded "<unixnano>.<uuid>". social owns the rows
// this orders; bff forwards cursors verbatim, so both sides need this identical wire format.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

func (c Cursor) String() string {
	return strconv.FormatInt(c.CreatedAt.UnixNano(), 10) + "." + c.ID.String()
}

// ParseCursor parses the "<unixnano>.<uuid>" wire format, splitting on the first dot only -
// a well-formed uuid tail never itself contains a dot.
func ParseCursor(s string) (*Cursor, error) {
	dot := strings.IndexByte(s, '.')
	if dot < 1 {
		return nil, fmt.Errorf("httpkit: malformed cursor")
	}
	nanos, err := strconv.ParseInt(s[:dot], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("httpkit: malformed cursor time")
	}
	id, err := uuid.Parse(s[dot+1:])
	if err != nil {
		return nil, fmt.Errorf("httpkit: malformed cursor id")
	}
	return &Cursor{CreatedAt: time.Unix(0, nanos), ID: id}, nil
}
