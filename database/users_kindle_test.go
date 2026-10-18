package database

import (
	"fmt"
	"testing"
	"time"

	"gopds-api/models"
)

// The Kindle columns are NOT NULL, and go-pg writes a Go zero as NULL unless a
// field says otherwise. These go through the real paths a profile takes —
// creating a reader, the full-row update behind change-me, the address
// endpoint — and check what the row holds afterwards.

func kindleReader(t *testing.T) models.User {
	t.Helper()
	name := fmt.Sprintf("kindle_%d", time.Now().UnixNano())
	id := makeUser(t, name)
	t.Cleanup(func() { _ = DeleteUser(fmt.Sprint(id)) })

	var user models.User
	if err := db.Model(&user).Where("id = ?", id).Select(); err != nil {
		t.Fatalf("reading the reader back: %v", err)
	}
	return user
}

// A reader created by the application starts with both buttons shown, no
// Kindle address and a sender id of their own. With use_zero alone the
// switches would have been inserted as false.
func TestNewReaderStartsWithKindleDefaults(t *testing.T) {
	requireDatabase(t)

	first := kindleReader(t)
	second := kindleReader(t)

	for _, user := range []models.User{first, second} {
		if !user.ShowKindleButton || !user.ShowTelegramButton {
			t.Errorf("%s starts with the buttons %v/%v, want both shown",
				user.Login, user.ShowKindleButton, user.ShowTelegramButton)
		}
		if user.KindleEmail != "" {
			t.Errorf("%s starts with the Kindle address %q", user.Login, user.KindleEmail)
		}
		if user.KindleSenderID == "" {
			t.Errorf("%s has no sender id", user.Login)
		}
	}
	if first.KindleSenderID == second.KindleSenderID {
		t.Errorf("two readers share the sender id %s", first.KindleSenderID)
	}
}

// The profile's switches go through the full-row update. Turning one off has
// to write false — not NULL, which the column refuses — and leave the other
// and everything else alone; a request that does not mention them changes
// neither.
func TestProfileUpdateTurnsTheButtonsOnAndOff(t *testing.T) {
	requireDatabase(t)
	user := kindleReader(t)
	off, on := false, true

	updated, err := UpdateUserProfile(user.ID, models.SelfUserChangeRequest{ShowKindleButton: &off})
	if err != nil {
		t.Fatalf("turning the Kindle button off: %v", err)
	}
	if updated.ShowKindleButton || !updated.ShowTelegramButton {
		t.Errorf("after turning Kindle off the buttons are %v/%v", updated.ShowKindleButton, updated.ShowTelegramButton)
	}

	// The profile's Save, which sends the name and nothing about the buttons.
	updated, err = UpdateUserProfile(user.ID, models.SelfUserChangeRequest{FirstName: "Аркадий"})
	if err != nil {
		t.Fatalf("saving the name: %v", err)
	}
	if updated.ShowKindleButton || !updated.ShowTelegramButton {
		t.Errorf("a Save that did not mention the buttons changed them to %v/%v",
			updated.ShowKindleButton, updated.ShowTelegramButton)
	}

	if _, err = UpdateUserProfile(user.ID, models.SelfUserChangeRequest{ShowTelegramButton: &off, ShowKindleButton: &on}); err != nil {
		t.Fatalf("flipping both: %v", err)
	}
	var stored models.User
	if err = db.Model(&stored).Where("id = ?", user.ID).Select(); err != nil {
		t.Fatal(err)
	}
	if !stored.ShowKindleButton || stored.ShowTelegramButton {
		t.Errorf("the row holds %v/%v, want Kindle shown and Telegram hidden",
			stored.ShowKindleButton, stored.ShowTelegramButton)
	}
	if stored.KindleSenderID != user.KindleSenderID {
		t.Errorf("a profile update changed the sender id from %s to %s", user.KindleSenderID, stored.KindleSenderID)
	}
}

// The address is set, read back for sending, and cleared — and a full-row
// profile update in between neither loses it nor trips over the empty one.
func TestKindleAddressSetReadAndCleared(t *testing.T) {
	requireDatabase(t)
	user := kindleReader(t)

	updated, err := SetKindleEmail(user.ID, "reader@kindle.com")
	if err != nil {
		t.Fatalf("setting the address: %v", err)
	}
	if updated.KindleEmail != "reader@kindle.com" || updated.Login != user.Login {
		t.Errorf("setting the address returned %q for %q", updated.KindleEmail, updated.Login)
	}

	if _, err = UpdateUserProfile(user.ID, models.SelfUserChangeRequest{LastName: "Стругацкий"}); err != nil {
		t.Fatalf("saving the profile: %v", err)
	}
	target, err := GetKindleTarget(user.ID)
	if err != nil {
		t.Fatalf("reading the target: %v", err)
	}
	if target.Email != "reader@kindle.com" || target.SenderID != user.KindleSenderID {
		t.Errorf("target is %+v, want the address and sender %s", target, user.KindleSenderID)
	}

	if _, err = SetKindleEmail(user.ID, ""); err != nil {
		t.Fatalf("clearing the address: %v", err)
	}
	if _, err = UpdateUserProfile(user.ID, models.SelfUserChangeRequest{FirstName: "Борис"}); err != nil {
		t.Fatalf("saving the profile with no address: %v", err)
	}
	if target, err = GetKindleTarget(user.ID); err != nil || target.Email != "" {
		t.Errorf("after clearing the target is %+v, %v", target, err)
	}
}

func TestSetKindleEmailForNobodyFails(t *testing.T) {
	requireDatabase(t)
	if _, err := SetKindleEmail(-1, "reader@kindle.com"); err == nil {
		t.Error("setting an address for a reader who does not exist succeeded")
	}
}

// Two writes in flight at once — here, one lands between the profile
// update's read and its write. The update used to write back the whole row it
// had read, so whichever finished second undid the other, both reporting
// success. Each must now leave the other's columns alone.
func TestProfileUpdateKeepsAConcurrentWrite(t *testing.T) {
	requireDatabase(t)
	off := false

	cases := []struct {
		name       string
		update     models.SelfUserChangeRequest
		concurrent string
		check      func(t *testing.T, stored models.User)
	}{
		{
			name:       "the other switch",
			update:     models.SelfUserChangeRequest{ShowKindleButton: &off},
			concurrent: `UPDATE auth_user SET show_telegram_button = false WHERE id = ?`,
			check: func(t *testing.T, stored models.User) {
				if stored.ShowKindleButton || stored.ShowTelegramButton {
					t.Errorf("the buttons are %v/%v, want both off", stored.ShowKindleButton, stored.ShowTelegramButton)
				}
			},
		},
		{
			name:       "a cleared Kindle address",
			update:     models.SelfUserChangeRequest{FirstName: "Аркадий"},
			concurrent: `UPDATE auth_user SET kindle_email = '' WHERE id = ?`,
			check: func(t *testing.T, stored models.User) {
				if stored.KindleEmail != "" || stored.FirstName != "Аркадий" {
					t.Errorf("the row holds %q / %q, want the address cleared and the name saved",
						stored.KindleEmail, stored.FirstName)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := kindleReader(t)
			if _, err := SetKindleEmail(user.ID, "reader@kindle.com"); err != nil {
				t.Fatal(err)
			}

			previous := profileReadHook
			t.Cleanup(func() { profileReadHook = previous })
			profileReadHook = func() {
				if _, err := db.Exec(tc.concurrent, user.ID); err != nil {
					t.Errorf("the concurrent write: %v", err)
				}
			}

			returned, err := UpdateUserProfile(user.ID, tc.update)
			if err != nil {
				t.Fatalf("updating the profile: %v", err)
			}
			profileReadHook = previous

			var stored models.User
			if err := db.Model(&stored).Where("id = ?", user.ID).Select(); err != nil {
				t.Fatal(err)
			}
			tc.check(t, stored)
			// What the caller is handed is the row as it stands, not as read.
			tc.check(t, returned)
			if stored.KindleSenderID != user.KindleSenderID {
				t.Errorf("the sender id changed")
			}
		})
	}
}
