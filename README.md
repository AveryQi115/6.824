# 6.824(sp 2022). 
  
- [x] mapreduce in [repo distributed_sys](https://github.com/Nicola115/distributed_sys)
- [x] lab 2A
- [x] lab 2B
- [x] lab 2C
- [x] lab 2D  

## Lab2D的心得  
  
- `chanApply`是一个不带缓冲区的channel，test在收满SnapshotInterval个Apply Command之后调用`Snapshot`，`applyLog`中对于`chanApply`的写入阻塞  
  
  所以如果在请求了锁的区域中写`chanApply`会导致锁迟迟不释放，test又调用`Snapshot`请求锁，导致死锁(go-deadlock debug出来的)
- test调用`Snapshot`,`Snapshot`生成快照写Apply msg，test接收到会将该server的lastApplied设置为快照的lastIncludedIndex，如果applyLog和Snapshot并发，在快照前发送的Apply command会被test丢弃，在快照后发送的apply command需要按照lastIncludedIndex+1的顺序往后  
  - 试过用`select case chanApply<-xx`写，但这会导致微小的阻塞也会使applyLog return，整体速度减慢，无法通过测试  
  - 试过用Snapshot发送停止applyLog信号，但是信号的延迟导致不定期产生apply out of order
- 最后的做法是test的代码本质是clerk，SnapshotInterval应该clerk和raft server都能感知，所以raft server主动发送到SnapshotInterval个command就停止
  - 这样的话原来的applyLog设计(每次更改CommitIndex调用applyLog就太慢了)需要改成一个goroutine定时发现applyLog
