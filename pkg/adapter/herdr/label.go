package herdr

import (
	"context"
	"strconv"

	"github.com/m-mizutani/goerr/v2"
)

// renamedTarget is the tab or the pane whose name octbox replaced with the
// unread count. Exactly one of tabID and pane is set.
type renamedTarget struct {
	tabID string
	pane  bool
	// original is the name before octbox replaced it. For a pane, nil means
	// the pane had no name.
	original *string
	// label is the name octbox set last.
	label string
}

func (x *renamedTarget) is(tabID string, pane bool) bool {
	return x.pane == pane && (pane || x.tabID == tabID)
}

type paneTarget struct {
	PaneID string `json:"pane_id"`
}

type tabTarget struct {
	TabID string `json:"tab_id"`
}

type paneRenameParams struct {
	PaneID string `json:"pane_id"`
	// Label nil clears the pane's name.
	Label *string `json:"label,omitempty"`
}

type tabRenameParams struct {
	TabID string `json:"tab_id"`
	Label string `json:"label"`
}

type paneInfoResult struct {
	Pane struct {
		TabID string  `json:"tab_id"`
		Label *string `json:"label"`
	} `json:"pane"`
}

type tabInfoResult struct {
	Tab struct {
		Label     string `json:"label"`
		PaneCount int    `json:"pane_count"`
	} `json:"tab"`
}

func unreadLabel(count int) string {
	return "[octbox] " + strconv.Itoa(count) + " unread"
}

// ShowUnreadCount names the tab after count when the pane is the only one in
// its tab, and names the pane instead when the tab has other panes. The
// layout is read at every call, so splitting the tab, closing the other
// panes, or moving the pane to another tab moves the count; the name it
// leaves is put back. A name is sent only when it differs from the one
// octbox set last, so a name the user sets is kept until the count changes.
func (x *Client) ShowUnreadCount(ctx context.Context, count int) error {
	var pane paneInfoResult
	if err := x.send(ctx, "pane.get", paneTarget{PaneID: x.paneID}, &pane); err != nil {
		return err
	}
	var tab tabInfoResult
	if err := x.send(ctx, "tab.get", tabTarget{TabID: pane.Pane.TabID}, &tab); err != nil {
		return err
	}

	label := unreadLabel(count)
	byPane := tab.Tab.PaneCount > 1
	if x.renamed != nil && x.renamed.is(pane.Pane.TabID, byPane) {
		if x.renamed.label == label {
			return nil
		}
		if err := x.rename(ctx, x.renamed, &label); err != nil {
			return err
		}
		x.renamed.label = label
		return nil
	}

	if err := x.RemoveUnreadCount(ctx); err != nil {
		return err
	}
	next := &renamedTarget{label: label}
	if byPane {
		next.pane = true
		next.original = pane.Pane.Label
	} else {
		next.tabID = pane.Pane.TabID
		next.original = &tab.Tab.Label
	}
	if err := x.rename(ctx, next, &label); err != nil {
		return err
	}
	x.renamed = next
	return nil
}

// RemoveUnreadCount puts back the name the count replaced. herdr cannot turn
// a tab back to its automatic name (its number), so a tab gets the same text
// as a name of its own. A tab closed since then needs nothing put back.
func (x *Client) RemoveUnreadCount(ctx context.Context) error {
	if x.renamed == nil {
		return nil
	}
	if err := x.rename(ctx, x.renamed, x.renamed.original); err != nil && !goerr.HasTag(err, tagTabNotFound) {
		return err
	}
	x.renamed = nil
	return nil
}

func (x *Client) rename(ctx context.Context, target *renamedTarget, label *string) error {
	if target.pane {
		return x.send(ctx, "pane.rename", paneRenameParams{PaneID: x.paneID, Label: label}, nil)
	}
	if label == nil {
		return goerr.New("a tab name cannot be cleared", goerr.V("tab_id", target.tabID))
	}
	return x.send(ctx, "tab.rename", tabRenameParams{TabID: target.tabID, Label: *label}, nil)
}
