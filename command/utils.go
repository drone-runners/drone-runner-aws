package command

import (
	"fmt"

	"gopkg.in/alecthomas/kingpin.v2"
)

// PrintAppDetails prints some information about the app
func PrintAppDetails(app *kingpin.Application) {
	fmt.Println("help: ", app.Help)
}
