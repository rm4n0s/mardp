package system

import "testing"

func TestSetLoginItem(t *testing.T) {
	tests := []struct {
		name    string
		code    int
		message string
	}{
		{"done", 0, ""},
		{"refused", 1, "Operation not permitted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, on := range []bool{true, false} {
				var asked []bool
				err := setLoginItem(on, func(on bool) (int, string) {
					asked = append(asked, on)
					return tt.code, tt.message
				})
				if len(asked) != 1 || asked[0] != on {
					t.Errorf("asked %v, want [%v]", asked, on)
				}
				if tt.code == 0 {
					if err != nil {
						t.Errorf("on %v: %v", on, err)
					}
					continue
				}
				assertRoute(t, err, "setLoginItem.LoginItemFailed")
			}
		})
	}
}

func TestLoginItemRegistered(t *testing.T) {
	want := map[LoginItemStatus]bool{LoginItemOff: false, LoginItemOn: true, LoginItemNeedsApproval: true, LoginItemNotFound: false}
	for st, w := range want {
		if st.Registered() != w {
			t.Errorf("%d: registered %v", st, !w)
		}
	}
}
