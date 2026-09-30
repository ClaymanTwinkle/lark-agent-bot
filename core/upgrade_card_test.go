package core

import "testing"

func cardButtonValues(card *Card) []string {
	var values []string
	for _, elem := range card.Elements {
		if actions, ok := elem.(CardActions); ok {
			for _, btn := range actions.Buttons {
				values = append(values, btn.Value)
			}
		}
	}
	return values
}

// The /upgrade card reached from /help used to only say "run /upgrade
// confirm", so clicking through the menu could never install anything.
func TestUpgradeCardOffersConfirmButtonWhenUpdateAvailable(t *testing.T) {
	e := newTestEngine()

	values := cardButtonValues(e.upgradeCard("v0.3.3 available", true))
	found := false
	for _, v := range values {
		if v == "cmd:/upgrade confirm" {
			found = true
		}
	}
	if !found {
		t.Fatalf("buttons = %v, want one that runs /upgrade confirm", values)
	}
}

func TestUpgradeCardHasNoConfirmButtonWithoutUpdate(t *testing.T) {
	e := newTestEngine()

	for _, v := range cardButtonValues(e.upgradeCard("up to date", false)) {
		if v == "cmd:/upgrade confirm" {
			t.Fatal("confirm button shown although no update is available")
		}
	}
}
