package raft

import (
	"6.824/labgob"
	"6.824/labrpc"
	"bytes"
	"time"
)
import "sync/atomic"
import "math/rand"
import "github.com/sasha-s/go-deadlock"



/************************************************************* basic *******************************************************/

//
// Raft server states.
//
const (
	STATE_CANDIDATE = iota
	STATE_FOLLOWER
	STATE_LEADER
)

type Raft struct {
	mu        deadlock.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead	  int32

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	state     int
	voteCount int

	// Persistent state on all servers.
	currentTerm int
	votedFor    int
	log         []LogEntry

	// Volatile state on all servers.
	commitIndex int
	lastApplied int

	// Volatile state on leaders.
	nextIndex  []int
	matchIndex []int

	// Channels between raft peers.
	chanApply     chan ApplyMsg					// commit LogEntry or Snapshot to service
	chanGrantVote chan bool						// follower/candidate receive request for vote
	chanWinElect  chan bool						// candidate wins election
	chanHeartbeat chan bool						// follower receive heartbeat
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	// Your code here (2A).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term := rf.currentTerm
	isleader := (rf.state == STATE_LEADER)
	return term, isleader
}

func (rf *Raft) Run() {
	for {
		switch rf.state {
		case STATE_FOLLOWER:
			select {
			case <-rf.chanGrantVote:
			case <-rf.chanHeartbeat:
			case <-time.After(time.Millisecond * time.Duration(rand.Intn(300)+200)):
				DPrintf("[rf.Run] server %v change to candidate\n",rf.me)
				rf.state = STATE_CANDIDATE
				rf.persist()
			}
		case STATE_LEADER:
			go rf.broadcastHeartbeat()
			time.Sleep(time.Millisecond * 60)
		case STATE_CANDIDATE:
			rf.mu.Lock()
			rf.currentTerm++
			rf.votedFor = rf.me
			rf.voteCount = 1
			rf.persist()
			rf.mu.Unlock()
			go rf.broadcastRequestVote()

			select {
			case <-rf.chanHeartbeat:
				rf.state = STATE_FOLLOWER
			case <-rf.chanWinElect:
			case <-time.After(time.Millisecond * time.Duration(rand.Intn(300)+200)):
			}
		}
	}
}




/************************************************************* Candidate RequestVote *******************************************************/

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	VoteGranted bool
}

// candidate broadcastRequestVote
func (rf *Raft) broadcastRequestVote() {
	rf.mu.Lock()
	args := &RequestVoteArgs{}
	args.Term = rf.currentTerm
	args.CandidateId = rf.me
	args.LastLogIndex = rf.getLastLogIndex()
	args.LastLogTerm = rf.getLastLogTerm()
	rf.mu.Unlock()

	for server := range rf.peers {
		if server != rf.me && rf.state == STATE_CANDIDATE {
			go rf.sendRequestVote(server, args, &RequestVoteReply{})
		}
	}
}

// candidate sendRequestVote to other peer
// handle reply
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()

	if ok {
		if rf.state != STATE_CANDIDATE || rf.currentTerm != args.Term {
			// invalid request
			return ok
		}
		if rf.currentTerm < reply.Term {
			// revert to follower state and update current term
			rf.state = STATE_FOLLOWER
			rf.currentTerm = reply.Term
			rf.votedFor = -1
			return ok
		}

		if reply.VoteGranted {
			rf.voteCount++
			if rf.voteCount > len(rf.peers)/2 {
				// win the election
				DPrintf("[rf.Run] server %v win election\n",rf.me)
				rf.state = STATE_LEADER
				rf.persist()
				rf.nextIndex = make([]int, len(rf.peers))
				rf.matchIndex = make([]int, len(rf.peers))
				nextIndex := rf.getLastLogIndex() + 1
				for i := range rf.nextIndex {
					rf.nextIndex[i] = nextIndex
				}
				rf.chanWinElect <- true
			}
		}
	}

	return ok
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()

	if args.Term < rf.currentTerm {
		// reject request with stale term number
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}

	if args.Term > rf.currentTerm {
		// become follower and update current term
		rf.state = STATE_FOLLOWER
		rf.currentTerm = args.Term
		rf.votedFor = -1
	}

	reply.Term = rf.currentTerm
	reply.VoteGranted = false

	if (rf.votedFor == -1 || rf.votedFor == args.CandidateId) && rf.isUpToDate(args.LastLogTerm, args.LastLogIndex) {
		// vote for the candidate
		rf.votedFor = args.CandidateId
		reply.VoteGranted = true
		rf.chanGrantVote <- true			// ignore message from stale term number, not take it as a heartbeat
	}
}


/********************************************************************** Leader HeartBeat *************************************************/


type AppendEntriesArgs struct{
	Term			int
	LeaderId 		int
	PrevLogIndex	int
	PrevLogTerm		int
	Entries			[]LogEntry
	LeaderCommit	int
}

type AppendEntriesReply struct{
	Term 	int
	Success bool
	NextTryIndex int
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()

	reply.Success = false

	if args.Term < rf.currentTerm {
		// reject requests with stale term number
		reply.Term = rf.currentTerm
		reply.NextTryIndex = rf.getLastLogIndex() + 1
		return
	}

	if args.Term > rf.currentTerm {
		// become follower and update current term
		rf.state = STATE_FOLLOWER
		rf.currentTerm = args.Term
		rf.votedFor = -1
	}

	// confirm heartbeat to refresh timeout
	rf.chanHeartbeat <- true

	reply.Term = rf.currentTerm

	// logEntries is too short, return last Index
	if args.PrevLogIndex > rf.getLastLogIndex() {
		reply.NextTryIndex = rf.getLastLogIndex() + 1
		return
	}

	baseIndex := rf.log[0].Index

	if args.PrevLogIndex >= baseIndex && args.PrevLogTerm != rf.log[args.PrevLogIndex-baseIndex].Term {
		// if entry log[prevLogIndex] conflicts with new one, there may be conflict entries before.
		// bypass all entries during the problematic term to speed up.
		term := rf.log[args.PrevLogIndex-baseIndex].Term
		for i := args.PrevLogIndex - 1; i >= baseIndex; i-- {
			if rf.log[i-baseIndex].Term != term {
				reply.NextTryIndex = i + 1
				break
			}
		}
	} else if args.PrevLogIndex >= baseIndex-1 {
		// otherwise log up to prevLogIndex are safe.
		// merge lcoal log and entries from leader, and apply log if commitIndex changes.
		rf.log = rf.log[:args.PrevLogIndex-baseIndex+1]
		rf.log = append(rf.log, args.Entries...)

		reply.Success = true
		reply.NextTryIndex = args.PrevLogIndex + len(args.Entries)

		if rf.commitIndex < args.LeaderCommit {
			// update commitIndex and apply log
			rf.commitIndex = min(args.LeaderCommit, rf.getLastLogIndex())
		}
	}

	//TODO: if prevLogIndex < baseIndex; do nothing
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// DPrintf("rf %v send log to %v, args=%+v,reply=%+v\n",rf.me,server,args,reply)

	if !ok || rf.state != STATE_LEADER || args.Term != rf.currentTerm {
		// invalid request
		return ok
	}
	if reply.Term > rf.currentTerm {
		// become follower and update current term
		rf.currentTerm = reply.Term
		rf.state = STATE_FOLLOWER
		rf.votedFor = -1
		rf.persist()
		return ok
	}

	if reply.Success {
		if len(args.Entries) > 0 {
			rf.nextIndex[server] = args.Entries[len(args.Entries)-1].Index + 1
			rf.matchIndex[server] = rf.nextIndex[server] - 1
		}
	} else {
		rf.nextIndex[server] = min(reply.NextTryIndex, rf.getLastLogIndex())
	}

	baseIndex := rf.log[0].Index
	for N := rf.getLastLogIndex(); N > rf.commitIndex && rf.log[N-baseIndex].Term == rf.currentTerm; N-- {
		// find if there exists an N to update commitIndex
		count := 1
		for i := range rf.peers {
			if i != rf.me && rf.matchIndex[i] >= N {
				count++
			}
		}
		if count > len(rf.peers)/2 {
			rf.commitIndex = N
			break
		}
	}

	return ok
}

//
// broadcast heartbeat to all followers.
// the heartbeat may be AppendEntries or InstallSnapshot depending on whether
// required log entry is discarded.
//
func (rf *Raft) broadcastHeartbeat() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	baseIndex := rf.log[0].Index
	snapshot := rf.persister.ReadSnapshot()

	for server := range rf.peers {
		if server != rf.me && rf.state == STATE_LEADER {
			// sending logEntries
			if rf.nextIndex[server] > baseIndex {
				args := &AppendEntriesArgs{}
				args.Term = rf.currentTerm
				args.LeaderId = rf.me
				args.PrevLogIndex = rf.nextIndex[server] - 1
				if args.PrevLogIndex >= baseIndex {
					args.PrevLogTerm = rf.log[args.PrevLogIndex-baseIndex].Term
				}
				if rf.nextIndex[server] <= rf.getLastLogIndex() {
					args.Entries = rf.log[rf.nextIndex[server]-baseIndex:]
				}
				args.LeaderCommit = rf.commitIndex
				go rf.sendAppendEntries(server, args, &AppendEntriesReply{})
			} else {
				DPrintf("rf %v send snapshot to %v, baseIndex=%v, rf.nextIndex[server]=%v\n",rf.me,server,baseIndex,rf.nextIndex[server])
				// sending snapshots
				args := &InstallSnapshotArgs{}
				args.Term = rf.currentTerm
				args.LeaderId = rf.me
				args.LastIncludedIndex = rf.log[0].Index
				args.LastIncludedTerm = rf.log[0].Term
				args.Data = snapshot
				go rf.sendInstallSnapshot(server, args, &InstallSnapshotReply{})
			}
		}
	}
}

/****************************************************************** handle logs **********************************************************************/


type LogEntry struct {
	Index   int						// redundant but convenient
	Term    int
	Command interface{}
}

func (rf *Raft) getLastLogTerm() int {
	return rf.log[len(rf.log)-1].Term
}

func (rf *Raft) getLastLogIndex() int {
	return rf.log[len(rf.log)-1].Index
}

func (rf *Raft) atIndex(index int) LogEntry{
	for _,log := range rf.log{
		if log.Index == index{
			return log
		}
	}
	return LogEntry{-1,-1,nil}
}

//
// check if candidate's log is at least as new as the voter.
//
func (rf *Raft) isUpToDate(candidateTerm int, candidateIndex int) bool {
	term, index := rf.getLastLogTerm(), rf.getLastLogIndex()
	return candidateTerm > term || (candidateTerm == term && candidateIndex >= index)
}


/******************************************************************** persistent ***************************************************/

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	CommandIndex int
	Command      interface{}

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	data := rf.getRaftState()
	rf.persister.SaveRaftState(data)
}

func (rf *Raft) getRaftState() []byte {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	return w.Bytes()
}

//
// get previous encoded raft state size.
//
func (rf *Raft) GetRaftStateSize() int {
	return rf.persister.RaftStateSize()
}

//
// restore previously persisted state.
// persistent state includes currentTerm, votedFor, logEntries
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	d.Decode(&rf.currentTerm)
	d.Decode(&rf.votedFor)
	d.Decode(&rf.log)
}

//
// apply log entries with index in range [lastApplied + 1, commitIndex]
//
func (rf *Raft) applyLog() {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	var maxI int
	if rf.log[0].Index==0{
		maxI = rf.log[0].Index+SnapShotInterval-1
	}else{
		maxI = rf.log[0].Index+SnapShotInterval
	}
	var i int
	for i = rf.lastApplied + 1; i <= min(rf.commitIndex,maxI); i++ {
		msg := ApplyMsg{}
		msg.CommandIndex = i
		msg.CommandValid = true
		if i-rf.log[0].Index >0 && i-rf.log[0].Index <len(rf.log) {
			msg.Command = rf.log[i-rf.log[0].Index].Command
			rf.chanApply<-msg
			DPrintf("rf %v apply log %v\n",rf.me,i)
		}else{
			break
		}
	}
	rf.lastApplied = i-1
}

/******************************************************************** snapshot ************************************************/

func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {
	// Your code here (2D).
	return true
}

func(rf *Raft) applySnapshot(valid bool, data []byte, term int, index int){
	rf.chanApply<-ApplyMsg{
		SnapshotValid: valid,
		Snapshot: data,
		SnapshotTerm: term,
		SnapshotIndex: index,
	}
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if index > rf.lastApplied{
		rf.lastApplied = index
	}
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
// service call Snapshot
// raft server trim its log before and including the index
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	DPrintf("rf %v generate snapshot: index=%v\n",rf.me,index)
	baseIndex, lastIndex := rf.log[0].Index, rf.getLastLogIndex()
	if index <= baseIndex || index > lastIndex {
		// can't trim log since index is invalid
		return
	}

	rf.trimLog(index)
	DPrintf("rf %v logs after trim: %+v\n",rf.me,rf.log)

	rf.persister.SaveStateAndSnapshot(rf.getRaftState(), snapshot)

	go rf.applySnapshot(true, snapshot, rf.log[0].Term,rf.log[0].Index)
}

//
// recover from previous raft snapshot.
//
func (rf *Raft) recoverFromSnapshot(snapshot []byte) {
	if snapshot == nil || len(snapshot) < 1 {
		return
	}

	var lastIncludedIndex int
	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)
	d.Decode(&lastIncludedIndex)

	rf.lastApplied = lastIncludedIndex
	rf.commitIndex = lastIncludedIndex
	rf.trimLog(lastIncludedIndex)
}

type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Data              []byte
}

type InstallSnapshotReply struct {
	Term int
}

func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	DPrintf("rf %v receive snapshot, args.Term=%v args.LastIncludeIndex=%v\n",rf.me,args.Term,args.LastIncludedIndex)
	if args.Term < rf.currentTerm {
		// reject requests with stale term number
		DPrintf("rf %v receive stale term number, current term=%v\n",rf.me,rf.currentTerm)
		reply.Term = rf.currentTerm
		return
	}

	if args.Term > rf.currentTerm {
		// become follower and update current term
		rf.state = STATE_FOLLOWER
		rf.currentTerm = args.Term
		rf.votedFor = -1
		rf.persist()
	}

	// confirm heartbeat to refresh timeout
	rf.chanHeartbeat <- true

	reply.Term = rf.currentTerm

	if args.LastIncludedIndex > rf.commitIndex {
		DPrintf("rf %v apply snapshot, last commitIndex=%v\n",rf.me,rf.commitIndex)
		rf.trimLog(args.LastIncludedIndex)
		rf.log[0].Term = args.LastIncludedTerm
		rf.persister.SaveStateAndSnapshot(rf.getRaftState(), args.Data)
		rf.commitIndex = args.LastIncludedIndex
		// send snapshot to kv server
		go rf.applySnapshot(true,args.Data,args.LastIncludedTerm,args.LastIncludedIndex)
	}
}

//
// discard old log entries up to lastIncludedIndex.
//
func (rf *Raft) trimLog(lastIncludedIndex int) {
	newLog := make([]LogEntry, 0)
	newLog = append(newLog, LogEntry{Index: lastIncludedIndex, Term: 0})

	for i := len(rf.log) - 1; i >= 0; i-- {
		if rf.log[i].Index == lastIncludedIndex{
			newLog[0].Term = rf.log[i].Term
			newLog = append(newLog, rf.log[i+1:]...)
			break
		}
	}
	rf.log = newLog
}

func (rf *Raft) sendInstallSnapshot(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	ok := rf.peers[server].Call("Raft.InstallSnapshot", args, reply)
	rf.mu.Lock()
	defer rf.mu.Unlock()

	lastIncludedIndex := args.LastIncludedIndex
	if !ok || rf.state != STATE_LEADER || args.Term != rf.currentTerm {
		// invalid request
		DPrintf("[rf.sendInstallSnapshot] invalid request\n")
		return ok
	}

	if reply.Term > rf.currentTerm {
		// become follower and update current term
		rf.currentTerm = reply.Term
		rf.state = STATE_FOLLOWER
		rf.votedFor = -1
		rf.persist()
		return ok
	}

	rf.nextIndex[server] = lastIncludedIndex + 1
	rf.matchIndex[server] = lastIncludedIndex
	DPrintf("[rf.sendInstallSnapshot] server %v update nextIndex %v and matchIndex %v\n",server,rf.nextIndex[server],rf.matchIndex[server])
	return ok
}



//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	// Your code here (2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	term, index := -1, -1
	isLeader := (rf.state == STATE_LEADER)

	if isLeader {
		term = rf.currentTerm
		index = rf.getLastLogIndex() + 1
		rf.log = append(rf.log, LogEntry{Index: index, Term: term, Command: command})
		rf.persist()
	}
	return index, term, isLeader
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func (rf *Raft) applyLogRoutine(){
	for{
		select{
			case <-time.After(time.Millisecond * time.Duration(rand.Intn(200))):
				go rf.applyLog()
		}
	}
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
// applyCh is for sending the ApplyMsg to the client
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.state = STATE_FOLLOWER
	rf.voteCount = 0

	rf.currentTerm = 0
	rf.votedFor = -1
	rf.log = append(rf.log, LogEntry{Term: 0})

	rf.commitIndex = 0
	rf.lastApplied = 0

	rf.chanApply = applyCh
	rf.chanGrantVote = make(chan bool, 100)
	rf.chanWinElect = make(chan bool, 100)
	rf.chanHeartbeat = make(chan bool, 100)

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	rf.recoverFromSnapshot(persister.ReadSnapshot())
	rf.persist()

	go rf.Run()
	go rf.applyLogRoutine()

	return rf
}
