package flowcmd

import (
  "fmt"
  "os"
  "path/filepath"
  "github.com/spf13/cobra"
  "hyperview.in/client/config"

  client "hyperview.in/client"
)

const (
  concurrency = 10
)



func FlowCommands() []*cobra.Command {
  var task string

  flow_cmd := &cobra.Command{
    Use: "run",
    Short: "Start a hyperflow task for a given repo and commit",
    //TODO: add command details
    Long: `start a hyperflow task for a given repo and commit`, 
    Run: func(cmd *cobra.Command, args []string) {
      current_dir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
  
      repo_name, _ := config.ReadRepoParams(current_dir, "REPO_NAME")
      branch_name, _ := config.ReadRepoParams(current_dir, "BRANCH_NAME")
      commit_id, _ := config.ReadRepoParams(current_dir, "COMMIT_ID")

      c, _ := client.New(current_dir)
      flow_id, new_commit_id, task_resp, err := c.RunTask(repo_name, branch_name, commit_id, task)
      _ = config.WriteRepoParams(current_dir, "FLOW_ID", flow_id)
      _ = config.WriteRepoParams(current_dir, "COMMIT_ID", new_commit_id)
  
    },
  }
  flow_cmd.Flags().StringVarP(&task, "task","t", "", "task command")

  
  var result []*cobra.Command
  result = append(result, flow_cmd)
  return result
}


