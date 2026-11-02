package main
import("fmt";"os";"time";"screenstudio-re/core")
func main(){r:=&core.Recorder{}; s,e:=r.Record("/tmp/opencode/record-repro"); fmt.Fprintln(os.Stderr,"start",s,e);if e!=nil{return};time.Sleep(4*time.Second);p,e:=r.Stop();fmt.Fprintln(os.Stderr,"stop",p,e)}
