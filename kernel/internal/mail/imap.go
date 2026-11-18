package mail

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Fetched is one message read from a mailbox.
type Fetched struct {
	UID  string // the IMAP UID or the POP3 UIDL
	Size int64
	Raw  []byte
	// HeaderOnly: the message was over the size limit, so Raw holds its header only
	HeaderOnly bool
}

// IMAPRequest is one read of a folder.
type IMAPRequest struct {
	Folder   string
	MarkSeen bool
	// AfterUID and UIDValidity are where the last read stopped. Baseline reads nothing: it only reports the folder's
	// UIDVALIDITY and last UID (the first poll, or after UIDVALIDITY changed).
	AfterUID    uint32
	UIDValidity uint32
	Baseline    bool
	Limit       int   // messages per read (25 when 0)
	MaxSize     int64 // a larger message is read as its header only (25 MB when 0)
}

// IMAPResult is what a read found.
type IMAPResult struct {
	UIDValidity uint32
	LastUID     uint32 // the newest UID read, or the folder's last UID for a baseline
	Messages    []Fetched
	// Reset: the folder's UIDVALIDITY differs from the request's, so nothing was read (a baseline instead)
	Reset bool
}

// FetchIMAP reads the messages after req.AfterUID in a folder, oldest first, with BODY.PEEK[] (nothing is marked
// seen unless MarkSeen), and never deletes, moves or expunges.
func FetchIMAP(ctx context.Context, s Server, cred Credentials, req IMAPRequest, o Options) (*IMAPResult, error) {
	if req.Folder == "" {
		req.Folder = "INBOX"
	}
	if req.Limit <= 0 {
		req.Limit = 25
	}
	if req.MaxSize <= 0 {
		req.MaxSize = 25 << 20
	}
	conn, err := o.connect(ctx, s)
	if err != nil {
		return nil, err
	}
	opts := &imapclient.Options{TLSConfig: o.tlsConfig(s.Host)}
	var c *imapclient.Client
	if s.Security == "starttls" {
		if c, err = imapclient.NewStartTLS(conn, opts); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrTLSRequired, s.Host, err)
		}
	} else {
		c = imapclient.New(conn, opts)
	}
	defer c.Close()
	if err := c.WaitGreeting(); err != nil {
		return nil, err
	}
	if err := c.Login(cred.Username, cred.Password).Wait(); err != nil {
		var ie *imap.Error
		if errors.As(err, &ie) {
			return nil, fmt.Errorf("%w: %v", ErrAuth, err)
		}
		return nil, err
	}
	sel, err := c.Select(req.Folder, &imap.SelectOptions{ReadOnly: !req.MarkSeen}).Wait()
	if err != nil {
		return nil, fmt.Errorf("folder %s: %w", req.Folder, err)
	}
	res := &IMAPResult{UIDValidity: sel.UIDValidity}
	if !req.Baseline && req.UIDValidity != sel.UIDValidity {
		req.Baseline, res.Reset = true, true
	}
	if req.Baseline {
		// the folder's last UID: UIDNEXT - 1, or a search for * when the server leaves UIDNEXT out
		if sel.UIDNext > 0 {
			res.LastUID = uint32(sel.UIDNext) - 1
		} else {
			var last imap.UIDSet
			last.AddNum(0) // *
			sd, err := c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{last}}, nil).Wait()
			if err != nil {
				return nil, fmt.Errorf("search: %w", err)
			}
			for _, u := range sd.AllUIDs() {
				res.LastUID = max(res.LastUID, uint32(u))
			}
		}
		c.Logout().Wait()
		return res, nil
	}
	// UID SEARCH UID n:* also answers the last message when its UID is below n; only UIDs above AfterUID count
	var set imap.UIDSet
	set.AddRange(imap.UID(req.AfterUID+1), 0) // 0 = *
	sd, err := c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{set}}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var uids []imap.UID
	for _, u := range sd.AllUIDs() {
		if uint32(u) > req.AfterUID {
			uids = append(uids, u)
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	res.LastUID = req.AfterUID
	if len(uids) > req.Limit {
		uids = uids[:req.Limit]
	}
	if len(uids) == 0 {
		c.Logout().Wait()
		return res, nil
	}
	// sizes first, so a huge message is read as its header only
	sizes, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, RFC822Size: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch sizes: %w", err)
	}
	size := map[imap.UID]int64{}
	for _, m := range sizes {
		size[m.UID] = m.RFC822Size
	}
	whole := &imap.FetchItemBodySection{Peek: true}
	header := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, Peek: true}
	for _, u := range uids {
		sec, headerOnly := whole, false
		if size[u] > req.MaxSize {
			sec, headerOnly = header, true
		}
		msgs, err := c.Fetch(imap.UIDSetNum(u), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{sec}}).Collect()
		if err != nil {
			return nil, fmt.Errorf("fetch %d: %w", u, err)
		}
		if len(msgs) == 0 { // expunged meanwhile
			res.LastUID = uint32(u)
			continue
		}
		res.Messages = append(res.Messages, Fetched{UID: fmt.Sprint(uint32(u)), Size: size[u], Raw: msgs[0].FindBodySection(sec), HeaderOnly: headerOnly})
		res.LastUID = uint32(u)
	}
	if req.MarkSeen && len(res.Messages) > 0 {
		var seen []imap.UID
		for _, m := range res.Messages {
			var n uint32
			fmt.Sscan(m.UID, &n)
			seen = append(seen, imap.UID(n))
		}
		if err := c.Store(imap.UIDSetNum(seen...), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
			return nil, fmt.Errorf("mark seen: %w", err)
		}
	}
	c.Logout().Wait()
	return res, nil
}
