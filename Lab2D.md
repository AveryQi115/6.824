Lab2D

- [ ] AppendEntries about whether to match the log and append new logs
- [ ] RequestVote about most up-to-date log
- [ ] Snapshot(index byte)
  - raft收到snapshot就把自己的所有log里面[:index+1)的部分改了
- [ ] CondInstllSnapshot
- [ ] InstallSnapshot
  - receiver set lastApplied to rf.basicIndex
- [ ] SaveStateAndSnapshot()
  
## 问题  
- test会在接受固定数量applied log之后阻塞chanApply的接收，发起snapshot；raft返回snapshot之后test将appliedIndex设为snapshotIndex
- 如何处理/重发在snapshot msg之前被阻塞的applied log



//TODO: default value