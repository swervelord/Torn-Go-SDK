// This intentionally invalid consumer must fail static type checking.
package main

import torn "github.com/swervelord/Torn-Go-SDK"

func main() {
	batch, _ := torn.NewBatch(torn.UserSelections.Profile)
	torn.AddSelection(batch, torn.FactionSelections.Basic)
}
