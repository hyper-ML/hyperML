package flow

import (  
  "io"

  "fmt"
  "time"

  "github.com/hyper-ml/hyperml/server/pkg/base"
  "github.com/hyper-ml/hyperml/server/pkg/config"

  "github.com/hyper-ml/hyperml/server/pkg/storage"
  tasks_pkg "github.com/hyper-ml/hyperml/server/pkg/tasks"
  db_pkg "github.com/hyper-ml/hyperml/server/pkg/db"
  ws "github.com/hyper-ml/hyperml/server/pkg/workspace"
) 


type FlowServer struct {
  db db_pkg.DatabaseContext
  fe FlowEngine	
  qs *queryServer
  obj storage.ObjectAPIServer 
  wsapi ws.ApiServer
  ns string
  quit chan int
}

func NewFlowServer(config *config.Config,
  db db_pkg.DatabaseContext, 
  obj storage.ObjectAPIServer,
  wsapi ws.ApiServer, 
  logger storage.ObjectAPIServer) (*FlowServer, error) {
    
  qs := NewQueryServer(db)
  fe, err := NewFlowEngine(qs, db, logger, config) 
  if err != nil {
    return nil, err
  }
  quit := make(chan int)
  
  go fe.master(quit)
  
  fs := &FlowServer {
    db: db,
    ns: config.K8.Namespace,
    qs: qs,
    fe: fe,
    quit: quit,
    obj: obj,
    wsapi: wsapi, 
  }

  return fs, nil
    
}

func (fs *FlowServer) Close() {
  close(fs.quit)
}
 
func errorCompletedTask() error{
  return fmt.Errorf("Invalid Status Update. The task is already completed.")
}

func errInvalidWorkerForTask(workerId string) error {
  return fmt.Errorf("Invalid worker for this task: %s", workerId)
}

func (fs *FlowServer) GetFlowAttr(flowId string) (*FlowAttrs, error) {
  return fs.qs.GetFlowAttr(flowId)
}


func (fs *FlowServer) RegisterWorker(flowId string, taskId string, ipaddr string) (*WorkerAttrs, error) {
  work_attr, err := fs.qs.registerW(flowId, taskId, ipaddr)
  
  if err != nil {
    return nil, fmt.Errorf("Failed to register worker, err: ", err)
  }

  return work_attr, nil
}


func (fs *FlowServer) DetachTaskWorker(workerId, flowId, taskId string) error {
  return fs.qs.DetachTaskWorker(workerId, flowId, taskId)
}
 

func (fs *FlowServer) LaunchFlow(repoName, branchName, commitId, cmdStr string, evars map[string]string) (*FlowAttrs, error) {
  flow_attrs, err := fs.fe.LaunchFlow(repoName, branchName, commitId, cmdStr, evars)
  if err != nil {
    return nil, err
  }
  
  return flow_attrs, nil
}

func (fs *FlowServer) UpdateWorkerTaskStatus(worker Worker, tsr *TaskStatusChangeRequest) (*TaskStatusChangeResponse, error) {
  flow_attrs, err := fs.updateWorkerTaskStat(worker.Id, tsr.Flow.Id, tsr.Task.Id, tsr.TaskStatus)
  if err != nil {
    return nil, err
  }

  return &TaskStatusChangeResponse {
    FlowAttrs: flow_attrs,
  }, nil
}

func (fs *FlowServer) updateWorkerTaskStat(workerId string, flowId string, taskId string, newStatus tasks_pkg.TaskStatus) (*FlowAttrs, error) {
  task_worker:= fs.qs.GetWorkerByTaskId(flowId, taskId) 
  
  if task_worker.Worker.Id != workerId {
    base.Log("[FlowServer.updateWorkerTaskStat] Invalid Worker error (flowId, taskId, workerId): ", flowId, taskId, workerId)
    return nil, errInvalidWorkerForTask(workerId)
  }

  return fs.updateTaskStatus(flowId, taskId, newStatus)
}  

func (fs *FlowServer) updateTaskStatus(flowId string, taskId string, newStatus tasks_pkg.TaskStatus) (*FlowAttrs, error) {
  base.Log("[FlowServer.updateTaskStatus] taskId, newStatus: ", taskId, newStatus)
  
  task_attrs, err  := fs.qs.GetTaskByFlowId(flowId, taskId)
  if err != nil {
    return nil, err
  }
  
  task_attrs.Status = newStatus

  switch s := newStatus; s {
  
  case tasks_pkg.TASK_CREATED:
    task_attrs.Created = time.Now()
  

  case tasks_pkg.TASK_COMPLETED:
    //TODO: should come in the request from worker
    task_attrs.Completed = time.Now()
  
  case tasks_pkg.TASK_INITIATED:
    if task_attrs.Completed.IsZero() {
      task_attrs.Started = time.Now()
    } else {
      return nil, errorCompletedTask() 
    }
  
  case tasks_pkg.TASK_FAILED:
    if task_attrs.Completed.IsZero() {
      task_attrs.Failed = time.Now()
    } else {
      return nil, errorCompletedTask()
    }
  } 

  if err := fs.qs.UpdateTaskByFlowId(flowId, *task_attrs); err == nil {
    return  fs.qs.GetFlowAttr(flowId) 
  }

  return nil, err
}


/*func (fs *FlowServer) StartWorker(flowId, taskId string) error {
  return fs.fe.StartFlow(flowId, taskId)
}*/

func (fs *FlowServer) GetFlowLogPath(flowId string) string {
  return  "logs/flows/" + flowId //+ ".log"
}

func (fs *FlowServer) GetTaskLogPath(taskId string) string {
  return  "flow/" + taskId //+ ".log"
}

func (fs *FlowServer) GetCommandLogPath(taskId string) string {
  return  "flows/" + taskId //+ ".log"
}

func (fs *FlowServer) GetTaskLog(flowId string) ([]byte, int, error) {
  //fs.obj.
  file_name := fs.GetFlowLogPath(flowId)
  return fs.obj.GetObject(file_name, 0, 0)
}

func getOutRepoName(flowId string) string {
  return "flow-" + string(flowId) + "-out"
}


func (fs *FlowServer) NewOutput(flow Flow) (*ws.Repo, *ws.Branch, *ws.Commit, error) {
  repo_name := getOutRepoName(flow.Id) 
  branch_name := "master"

  repo_attrs, err:= fs.wsapi.InitRepo(repo_name)
  if err != nil {
    return nil, nil, nil, err
  }

  branch := &ws.Branch{ Name: branch_name }
  commit_attrs, err:= fs.wsapi.InitCommit(repo_name, branch_name, "")

  return repo_attrs.Repo, branch, commit_attrs.Commit, nil
}

func (fs *FlowServer) GetOutput(flow Flow) (*ws.Repo, *ws.Branch, *ws.Commit, error) {
  
  if flow.Id == "" { 
    return nil, nil, nil, fmt.Errorf("Invalid flow ID")
  }

  base.Info("[FlowServer.GetOutput] GetOutput: ", flow)
  repo_name := getOutRepoName(flow.Id)

  base.Debug("[FlowServer.GetOutput] Out Repo : ", repo_name)
  branch_name := "master"

  if !fs.wsapi.CheckRepoExists(repo_name) {
    base.Debug("[FlowServer.GetOutput] The flow does not have any output stored: ", flow.Id)
    return &ws.Repo{}, &ws.Branch{}, &ws.Commit{}, nil
  }

  repo_attrs, _ := fs.wsapi.GetRepoAttrs(repo_name)
  branch_attrs, _ := fs.wsapi.GetBranchAttrs(repo_name, branch_name)

  return repo_attrs.Repo, branch_attrs.Branch, branch_attrs.Head, nil
}

func (fs *FlowServer) GetOrCreateOutput(flow Flow) (*ws.Repo, *ws.Branch, *ws.Commit, error) {
  repo_name := getOutRepoName(flow.Id)

  if !fs.wsapi.CheckRepoExists(repo_name) { 
    return fs.NewOutput(flow)
  } 
  
  return fs.GetOutput(flow) 
}

func getModelRepoName(flowId string) string {
  return "flow-" + flowId + "-model"
}

func (fs *FlowServer) GetModel(flow Flow) (repo *ws.Repo, branch *ws.Branch, commit *ws.Commit, fnErr error) {
  
  if flow.Id == "" {
    fnErr = fmt.Errorf("Invalid flow ID")
    return 
  }

  repo_name := getModelRepoName(flow.Id)
  branch_name := "master"

  if !fs.wsapi.CheckRepoExists(repo_name) {
    return &ws.Repo{}, &ws.Branch{}, &ws.Commit{}, nil
  }

  repo_attrs, _ := fs.wsapi.GetRepoAttrs(repo_name)
  branch_attrs, _ := fs.wsapi.GetBranchAttrs(repo_name, branch_name)

  return repo_attrs.Repo, branch_attrs.Branch, branch_attrs.Head, nil
}

func (fs *FlowServer) NewModel(flow Flow) (*ws.Repo, *ws.Branch, *ws.Commit, error) {
  repo_name   := getModelRepoName(flow.Id) 
  branch_name := "master"

  repo_attrs, err:= fs.wsapi.InitRepo(repo_name)
  if err != nil {
    base.Log("[FlowServer.NewModel] Failed to create model repo: ", err)
    return nil, nil, nil, err
  }
  
  branch := &ws.Branch{ Name: branch_name }
  commit_attrs, err:= fs.wsapi.InitCommit(repo_name, branch_name, "")

  return repo_attrs.Repo, branch, commit_attrs.Commit, nil
}

func (fs *FlowServer) GetOrCreateModel(flow Flow)  (repo *ws.Repo, branch *ws.Branch, commit *ws.Commit, fnErr error) {
  
  repo_name := getOutRepoName(flow.Id)

  if !fs.wsapi.CheckRepoExists(repo_name) { 
    return fs.NewModel(flow)
  } 
  
  return fs.GetModel(flow) 
}


func (fs *FlowServer) LogStream(flow_id string) (io.ReadCloser, error) {
  return fs.fe.LogStream(flow_id)
}


