package main

import (
	"fmt"
)

var cmdRedeploy = &Command{
	Run:        runRedeploy,
	Usage:      "redeploy [-y]",
	NeedsStack: true,
	Category:   "stack",
	Short:      "redeploys stack",
	Long: `Enqueues redeployment of the stack.
If the stack is already building, another build will be enqueued and performed immediately
after the current one is finished.

-y answers yes to confirmation question if the stack is production.
`,
}

var flagConfirmation bool

func init() {
	cmdRedeploy.Flag.BoolVar(&flagConfirmation, "y", false, "answer yes")
}

func runRedeploy(cmd *Command, args []string) {
	stack := mustStack()

	// confirmation is needed if the stack is production
	if stack.Environment == "production" && !flagConfirmation {
		mustConfirm("This is a production stack. Proceed with deployment? [yes/N]", "yes")
	}
	result, err := client.RedeployStack(stack.Uid)
	if err != nil {
		printFatal(err.Error())
	} else {
		fmt.Println(result.Message)
	}
}
