package workspace



import(
  "fmt"
  "time"
  "strings"
  path_util "path"
  "github.com/gobwas/glob"
  "hyperview.in/server/core/utils"
  "hyperview.in/server/base"

  "hyperview.in/server/core/db"
)

const (
  defaultBranch = "master"
)

type commitTxn struct {
  repoName string
  branchName string
  commitAttrs *CommitAttrs 
  db *db.DatabaseContext
  q *queryServer
}

func NewCommitTxn(repoName string, branchName string, commitId string, db *db.DatabaseContext) (*commitTxn, error) {

  var branch_name string = branchName

  q:= NewQueryServer(db)

  if branch_name == "" { 
    if commitId == "" { 
      return nil, fmt.Errorf("Invalid branch: %s", branch_name)
    } else {
      base.Info("[NewCommitTxn] Defaulting branch: ", defaultBranch)
      branch_name = defaultBranch
    }
  } 

  txn := &commitTxn {
    repoName: repoName,
    branchName: branch_name,
    db: db,
    q: q,
  }

  // validate and assign
  if commitId != "" {

    c_attrs, err := q.GetCommitAttrsById(repoName, commitId)
    if (err != nil ){
      return nil, fmt.Errorf("Invalid Commit Id or Repo Name: %s", err)
    }

    // if commit id not branch head then raise error 
    is_head, err := q.IsBranchHead(repoName, branch_name, commitId) 

    if err != nil {
      base.Debug("[NewCommitTxn] Failed to check branch head: ", err)
      return nil, err
    }

    if !is_head {
      return nil, errStaleCommit()
    }
    
    if c_attrs.IsOpen(){
      txn.commitAttrs = c_attrs
    }
    
  }
   
  return txn, nil
}



func (ct *commitTxn) setCommitAttrs(c *CommitAttrs) {
 ct.commitAttrs = c 
}

func (ct *commitTxn) IsOpenCommit() bool {
  if !ct.commitAttrs.IsOpen() {
    base.Log("This repo has no open commit. Please initialize commit before adding files.")
    return false
  }
  return true
}

func (ct *commitTxn) setCommitAttrsByBranch() error {
  commit_attrs, err := ct.q.GetCommitAttrsByBranch(ct.repoName, ct.branchName)
  if err != nil {
    return err
  }
  ct.commitAttrs = commit_attrs
  return nil
}

func (ct *commitTxn) Start() (string, error) {
  commit_attrs, err := ct.Init()
  return commit_attrs.Id(), err
}

func (ct *commitTxn) Init() (*CommitAttrs, error) {

  var err error 
  var branch_attr *BranchAttrs
  var repo_attrs *RepoAttrs
  var new_cinfo *CommitAttrs
  var head_cinfo *CommitAttrs
  var repo_name string = ct.repoName
  var branch_name string = ct.branchName

  // if commit info was already set in NewCommitTxn()
  if ct.commitAttrs != nil {
    return ct.commitAttrs, nil
  }

  repo_attrs, err = ct.q.GetRepoAttrs(ct.repoName)
  if (err !=nil) {
    base.Log("InitiateCommit: Could not fetch repo with given name %s", ct.repoName)
    return nil, err
  }

  // if this is first ever commit. create master branch
  if len(repo_attrs.Branches) == 0 { 
    return nil, errBranchMissing(repo_name + ":" + branch_name)
  } 
  
  branch_attr, err = ct.q.GetBranchAttrs(repo_name, branch_name)
  if err != nil {
    base.Log("[commitTxn.Init] Failed to retrieve branch: ", err)
    return nil, err
  }

  // check if there is an open commit then use it 
  if branch_attr.Head != nil  {
    head_cinfo, err = ct.q.GetCommitAttrsById(repo_name, branch_attr.Head.Id)
      
    if head_cinfo.IsOpen() {
        base.Log("There is a pending commit against this repo. Picking the same")
        ct.setCommitAttrs(head_cinfo)
        return head_cinfo, nil
    }
  }
  
  // create an open commit now that you have reached here
  if branch_attr != nil {

    // add commit with current head as parent 
    new_cinfo, err = ct.addCommit(branch_attr.Head)

    // update branch head with new commit  
    err = ct.scoopHead(branch_attr, new_cinfo.Commit)

    if err != nil {
      //TODO : delete new commit 
      defer ct.Delete()
      return nil, err
    }

    if new_cinfo != nil {
      ct.setCommitAttrs(new_cinfo)
      return new_cinfo, err
    }
  } 

  return nil, err
}

func (ct *commitTxn) addBranch(name string) (*BranchAttrs, error) {
  var err error 
  //var repo_attrs *RepoAttrs

  repo := &Repo {
    Name: ct.repoName,
  }
  
  branch := &Branch {
    Name: name,
    Repo: repo,
  }

  branch_attr := &BranchAttrs{
    Branch: branch,
    //Head: commit,
  }

  err = ct.q.InsertBranchAttrs(repo.Name, ct.branchName, branch_attr)

  if err != nil {
    return nil, err
  }

  //TODO: send context of error
  err = ct.q.AssignBranch(repo.Name, branch)

  if err != nil {
    return nil, err
  }

  return branch_attr, err
}
  
func (ct *commitTxn) addFileMap(commit *Commit, parent *Commit) (error) {
  var err error
  var fm *FileMap = NewFileMap(commit)

  if parent != nil {
    if parent.Id != "" {
      fm, err = ct.q.GetFileMap(ct.repoName, parent.Id)
      if err != nil {
        fmt.Println("err in get file map:", err)
        return err
      }
    }
  }

  return ct.q.InsertFileMap(ct.repoName, commit.Id, fm)
}

func (ct *commitTxn) addCommit(parent *Commit) (*CommitAttrs, error) {
  
  var commit_attrs *CommitAttrs
  var err error
  
  commit_id := utils.NewUUID()
  repo:= NewRepo(ct.repoName)

  commit := &Commit {
    Id: commit_id,
    Repo: repo,
  }

  commit_attrs = &CommitAttrs {
    Commit: commit,
    Parent_commit: parent,
    Started: time.Now(),
  }

  err = ct.q.InsertCommitAttrs(ct.repoName, commit_id, commit_attrs)
  if err != nil {
    //TODO: may be delete commit info
    return nil, err
  }
  
  if err = ct.addFileMap(commit, parent); err!= nil {
    ct.FlushCommit()
    return nil, err
  }

  return commit_attrs, err
}

func (ct *commitTxn) scoopHead(branchInfo *BranchAttrs, commit *Commit) error {
  branch := branchInfo.Branch
  repo := branchInfo.Branch.Repo

  branchInfo.Head = commit

  err:= ct.q.UpdateBranchAttrs(repo.Name, branch.Name, branchInfo)

  return err
}

func (ct *commitTxn) getSize() int64 {
  var size int64 
  repo_name := ct.repoName
  branch_name := ct.branchName
  var commit_id string
  
  if ct.commitAttrs != nil {
    commit_id = ct.commitAttrs.Id()
  }

  if commit_id == "" || repo_name == "" {
    base.Warn("[commitTxn.getCommitSize] Failed to get size of un-initialized commit txn.")
    return size
  }

  file_map, _ := ct.q.GetFileMap(repo_name, commit_id)

  if len(file_map.Entries) == 0 {
    return size
  }

  for fname, _ := range file_map.Entries {
    f_attrs, err := ct.q.GetFileAttrs(repo_name, commit_id, fname)
    if err != nil {
      base.Debug("[commitTxn.GetCommitSize] Failed to find size of file: ", repo_name, commit_id, fname)
      continue
    }
    size = size + f_attrs.Size()
  }

  base.Info("[commitTxn.GetSize] Size of Repo: ", size, repo_name, branch_name, commit_id)
  return size
}

func (ct *commitTxn) End() error {
  var err error 
  if (ct.commitAttrs == nil) {
    base.Log("finishCommit: Could not fetch any open commit for repo %s", ct.repoName)
    return fmt.Errorf("finishCommit: Could not fetch any open commit for repo %s", ct.repoName)
  }


  if ct.commitAttrs.IsOpen() {
    
    ct.commitAttrs.Finished = time.Now()
    ct.commitAttrs.Size = ct.getSize()

    err = ct.q.UpdateCommitAttrs(ct.repoName, ct.commitAttrs.Id(), ct.commitAttrs)
    return err  
  } else {
    base.Log("finishCommit: No open commit for this repo", ct.repoName)
    return fmt.Errorf("No open commit for this repo: %s", ct.repoName)
  }
  
}

func (ct *commitTxn) insertFileAttrs(filePath string, object string, size int64, cs string) (*FileAttrs, error) {
  var err error

  file_attrs := NewFileAttrs(ct.commitAttrs.Commit, filePath, object, size, cs)

  //TODO: get file info in return
  err = ct.q.UpsertFileAttrs(ct.repoName, ct.commitAttrs.Id(), filePath, file_attrs) 
  if err != nil {
    base.Log("Failed to update file map:", filePath, object, size)
    return nil, err 
  }

  err= ct.updateFileMap(filePath)
  if err != nil {
    base.Log("Failed to update file map:", filePath, object, size)
    return nil, err
  }

  return file_attrs, nil
}


func (ct *commitTxn) insertDirInfo(filePath string, size int64) (*FileAttrs, error) {
  var err error
  dir_info := NewDirInfo(ct.commitAttrs.Commit, filePath, size)
  err = ct.q.UpsertFileAttrs(ct.repoName, ct.commitAttrs.Id(), filePath, dir_info) 

  if err != nil {
    return nil, err 
  }
  err= ct.updateFileMap(filePath)

  if err != nil {
    base.Log("Failed to update file map:", filePath, size)
    return nil, err
  }
  
  return dir_info, nil 
}

func (ct *commitTxn) updateFileMap(filePath string) error {
  newfile := &File{Commit: ct.commitAttrs.Commit, Path: filePath}
  return ct.q.AddFileToMap(ct.repoName, ct.commitAttrs.Id(), newfile)
}

func (ct *commitTxn) AddFile(filePath string, objectName string, size int64, cs string) (*FileAttrs, error) {

  if (ct.commitAttrs == nil) {
    base.Log("Please initiate commit transaction with start-commit first.", ct.repoName)
    return nil, fmt.Errorf("Please initiate commit transaction with start-commit first.")
  }

  if !ct.commitAttrs.Finished.IsZero() {
    return nil, fmt.Errorf("This repo has no open commit. Please initialize commit before adding files.")
  }

  if objectName == "" {
    return ct.insertDirInfo(filePath, size)
  }

  return ct.insertFileAttrs(filePath, objectName, size, cs)
}

func (ct *commitTxn) AddDir(filePath string, size int64) (*FileAttrs, error) {

  // TODO: get the latest commit info to avoid concurrency issues
  if !ct.commitAttrs.Finished.IsZero() {
    return nil, fmt.Errorf("This repo has no open commit. Please initialize commit before adding files.")
  }
  
  return ct.insertDirInfo(filePath, size)
}

func (ct *commitTxn) FlushCommit() error{
  //delete commit and the file map
  return ct.Delete()
}


func (ct *commitTxn) Delete() error {
  // delete commit 
  var err error
  var branch_attr *BranchAttrs

  if ct.commitAttrs == nil {
    if err = ct.setCommitAttrsByBranch(); err != nil {
      return err
    }
  }

  if !ct.IsOpenCommit() {
    return fmt.Errorf("This repo has no open commit to flush")
  } 

  if ct.commitAttrs.Parent_commit != nil {
    branch_attr, err = ct.q.GetBranchAttrs(ct.repoName, ct.branchName)
    if err != nil {
      base.Log("Invalid repo or branch name:", ct.repoName, ct.branchName)
      return err
    }
    if err:= ct.scoopHead(branch_attr, ct.commitAttrs.Parent_commit); err!= nil {
      base.Log("Unable to scoop branch head to parent:", ct.commitAttrs.Parent_commit.Id)
      return err
    }

  }

  return ct.q.DeleteCommitAttrs(ct.repoName, ct.commitAttrs.Id())
}


// list files and sub directories given a directory path
//
func (ct *commitTxn) lsDir(list map[string]*File, prefix string) (map[string]*FileAttrs, error) {

  result:= make(map[string]*FileAttrs)
  
  /* Commented as client should send root path
  if prefix == "" {
    prefix = "/"
  }*/
  
  fmt.Println("prefix:", prefix)

  if prefix !="" && prefix[len(prefix)-1:] == "*"   {
    prefix  = prefix[:len(prefix)-1]
  }
  
  glob_pattern := prefix

  if glob_pattern[len(glob_pattern)-1:] != "/"   {
    glob_pattern  = glob_pattern + "/"
    prefix = prefix + "/"
  }

  g := glob.MustCompile(glob_pattern + "*")

  // / root doesnt work

  for path, file := range list {
    if g.Match(path) { 

      var path_splits []string
      var path_woprefix string
      
      //path without prefix 
      if prefix != "/" { 
        path_woprefix = strings.Replace(path, prefix, "", -1)
      } else {
        path_woprefix = path[1:] 
      }
 
      if len(path_woprefix) > 0 {
        path_splits = strings.SplitAfter(path_woprefix, "/")
      }

      if len(path_splits) >0 { 
        path_woslash := strings.Replace(path_splits[0], "/","", -1)

        if len(path_woslash) > 0 {

          if path_woslash == path_util.Base(file.Path) {
            file_attrs, err := ct.q.GetFileAttrs(file.Commit.Repo.Name, file.Commit.Id, path)
            
            if err == nil {
              result[path_woslash] = file_attrs
            } else {
              base.Log("something wrong. File Info missing for file: %s %s %s", file.Commit.Repo.Name, file.Commit.Id, path)
            } 

          } else {
            fmt.Println("creating directory")
            dir_info := NewDirInfo(file.Commit, path_woslash, 0)
            result[path_woslash] = dir_info
          } 

          // for directory create a new file info object and respond 
        }
      }
    }
      
  }
 
  return result, nil
}

// list directory path
func (ct *commitTxn) ListDir(dirPath string) (map[string]*FileAttrs, error) {
  
  if ct.commitAttrs == nil {
    return nil, fmt.Errorf("Missing Commit Info. Please start commit transaction with Id or start a new commit.")
  }

  fm, err := ct.q.GetFileMap(ct.repoName, ct.commitAttrs.Id())
  if err != nil {
    return nil, fmt.Errorf("Commit has not files or dirs to list")
  }

  return ct.lsDir(fm.Entries, dirPath)
}

// handle full path or just look at base path?

func (ct *commitTxn) LookupFile(fpath string) (*FileAttrs, error) {

  if ct.commitAttrs == nil {
    return nil, fmt.Errorf("[commitTxn.LookupFile] Missing Commit Info. Please start commit transaction with Id or start a new commit.")
  }

  fm, err := ct.q.GetFileMap(ct.repoName, ct.commitAttrs.Id())
  if err != nil || fm == nil {
    return nil, fmt.Errorf("[commitTxn.LookupFile] Commit has not files or dirs to list")
  }
  
  base.Debug("[commitTxn.LookupFile] fpath parameter - ", fpath)

  if fe := fm.Entries[fpath]; fe != nil {
    base.Debug("[commitTxn.LookupFile] found file in entries - ", fpath, ct.commitAttrs.Id())

    file_attrs, err := ct.q.GetFileAttrs(ct.repoName, fe.Commit.Id, fe.Path)
    

    if err == nil {
      base.Debug("[commitTxn.LookupFile] File Info of file found", file_attrs.File.Path)
      return file_attrs, nil
    } else if !base.IsErrFileNotFound(err) {
      base.Debug("[commitTxn.LookupFile] Unknown Error while looking for file. ", err)
      return nil, err
    }
  }
  
  // check if input name is a directory

  glob_pattern := fpath

  g := glob.MustCompile(glob_pattern + "/*") 

  for p, _ := range fm.Entries {   
    if g.Match(p) { 
      dir_info := NewDirInfo(ct.commitAttrs.Commit, fpath, 0)
      return dir_info, nil
    }
  }

  return nil, &base.ErrFileNotFound{CommitId: ct.commitAttrs.Id(), RepoName: ct.repoName, Fpath: fpath}
}





