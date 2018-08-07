package workspace


import (
  "fmt"
	"testing"
)



func Test_ListVirtualDir(t *testing.T) {
  d, _ := fake_db()
 
  vfs:= NewVfsServer(d)
  files, err := vfs.ListDir(test_repo_name, "", "/workspace/")
  
  if err != nil {
    fmt.Printf("Failed to list directories from an open commit: %s %e", test_repo_name, err)
    t.Fatalf("Failed to list directories from an open commit: %s %s", test_repo_name, err)
  }

  fmt.Println("files", files)
}


func Test_ListVirtualDir(t *testing.T) {
  d, _ := fake_db()
 
  vfs:= NewVfsServer(d)
  f_info, err := vfs.Lookup(test_repo_name, "", "/workspace/")
  
  if err != nil {
    fmt.Printf("Failed to list directories from an open commit: %s %e", test_repo_name, err)
    t.Fatalf("Failed to list directories from an open commit: %s %s", test_repo_name, err)
  }

  fmt.Println("f_info", f_info)
}