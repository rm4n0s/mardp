package system

import "github.com/rm4n0s/errors"

// TagLoginItemFailed: macOS refused to add mardp to the login items, or
// to take it out; its message is macOS's, and its metadata has "on" (what
// was asked) and "code" (the NSError's code).
const TagLoginItemFailed = "LoginItemFailed"

// LoginItemStatus says whether mardp opens at login (SMAppServiceStatus).
type LoginItemStatus int

// The states of the login item, as SMAppService has them.
const (
	// LoginItemOff: mardp is not a login item.
	LoginItemOff LoginItemStatus = iota
	// LoginItemOn: mardp opens at login.
	LoginItemOn
	// LoginItemNeedsApproval: mardp is a login item, but the user turned it
	// off, or has not allowed it yet, in System Settings › General › Login
	// Items.
	LoginItemNeedsApproval
	// LoginItemNotFound: macOS does not find the app to open, as when mardp
	// does not run from an app bundle.
	LoginItemNotFound
)

// Registered says that mardp asked to be a login item: it opens at login,
// or will once the user allows it.
func (s LoginItemStatus) Registered() bool {
	return s == LoginItemOn || s == LoginItemNeedsApproval
}

// LoginItem adds the running app to the login items and takes it out
// (SPEC §7.3), through SMAppService's main app service.
type LoginItem struct{}

// setLoginItem asks call to make the app a login item or not, which
// returns 0, or the NSError's code and message.
func setLoginItem(on bool, call func(on bool) (code int, message string)) error {
	if code, message := call(on); code != 0 {
		return errors.New(TagLoginItemFailed, message, "on", on, "code", code)
	}
	return nil
}
