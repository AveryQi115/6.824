package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"6.824/labgob"
	"bytes"
	"math/rand"
	"sort"

	//	"bytes"
	"sync"
	"sync/atomic"
	"time"

	//	"6.824/labgob"
	"6.824/labrpc"
)


//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type ElectionState int8

const(
	LEADER = ElectionState(0)
	CANDIDATE = ElectionState(1)
	FOLLOWER = ElectionState(2)
)

type LogEntry struct {
	Term    int
	Command interface{}
}


//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	currentTerm		int					// mark the current election term
	votedFor		int					// mark the leader server votedFor
	votes			int					// mark the committed votes for self
	state			ElectionState		// the current state of the server
	lastHeartBeat	time.Time			// last time the server receives heart beat msg
	stopElectionCheck chan int			// kill check go routine

	// attributes relative to logs
	logEntries		[]LogEntry			// log entries Raft server maintain
	commitIndex		int					// index of highest log entry known to be committed
	lastApplied		int					// index of highest log entry applied to state machine

	// attributes used by leader
	nextIndex		[]int				// for each server,index of the next log entry to send
	matchIndex		[]int				// for each server,index of highest log entry known to be replicated
	stopLeaderAction chan bool			// Done signal for the SendHeartBeat Process
	applyChan		chan ApplyMsg		// applyChan for sending committed command reply back to client

	// attributes used to store snapshot baseIndex
	basicIndex		int					// basicIndex when a snapshot is created
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	var term int
	var isleader bool
	// Your code here (2A).
	rf.mu.Lock()
	term = rf.currentTerm
	isleader = rf.state==LEADER
	rf.mu.Unlock()
	return term, isleader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)

	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.logEntries)

	data := w.Bytes()

	//log.Printf("[rf.persist] persist data = %v\n",data)
	rf.persister.SaveRaftState(data)
}


//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	rf.mu.Lock()
	d.Decode(&rf.currentTerm)
	d.Decode(&rf.votedFor)
	d.Decode(&rf.logEntries)
	rf.mu.Unlock()
}


//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).
	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
	rf.mu.Lock()
	defer rf.mu.Unlock()
}

type AppendEntriesArgs struct{
	Term			int
	LeaderID 		int
	PrevLogIndex	int
	PrevLogTerm		int
	Entries			[]LogEntry
	LeaderCommit	int
}

type AppendEntriesReply struct{
	Term 	int
	Success bool
	XTerm	int
	XIndex	int
	Len		int
}

// SendHeartBeat send empty AppendEntries messages
// happens along with an active leader
// 1.(concurrent) Done if the leader change the state
// 2.(concurrent) if the leader is outdated, call ConvertToFollower
// 3.(concurrent) wait for 100 millisecond, go on next cycle
func (rf *Raft) SendHeartBeat(){
	rf.mu.Lock()
	me := rf.me
	peerCount := len(rf.peers)
	rf.mu.Unlock()

	for{
		select{
		case <- rf.stopLeaderAction:
			return
		case <- time.After(45*time.Millisecond):
			i := 0
			for i < peerCount{
				rf.mu.Lock()
				if rf.state != LEADER{
					rf.mu.Unlock()
					return
				}
				// fake update nextIndex and matchIndex when server is me
				if rf.me == i{
					rf.matchIndex[i] = rf.nextIndex[i]-1
					if rf.nextIndex[i] < rf.basicIndex+len(rf.logEntries){
						rf.nextIndex[i] = rf.basicIndex+len(rf.logEntries)
					}
					rf.mu.Unlock()
					i+=1
					continue
				}

				currentTerm := rf.currentTerm
				commitIndex := rf.commitIndex
				prevLogIndex := rf.nextIndex[i] - 1
				if prevLogIndex > rf.basicIndex+len(rf.logEntries){
					DPrintf("[rf.SendHeartBeat] leader = %v, prevLogIndex > len(rf.logEntries),i=%v,prevLogIndex=%v,basicIndex=%v,len(rf.logEntries)=%v\n",rf.me,i,prevLogIndex,rf.basicIndex,len(rf.logEntries))
					prevLogIndex = rf.basicIndex+len(rf.logEntries)-1
					rf.nextIndex[i] = rf.basicIndex+len(rf.logEntries)
				}
				if prevLogIndex < rf.basicIndex{
					DPrintf("[rf.SendHeartBeat] leader=%v, basicIndex=%v, receiver=%v, prevLogIndex=%v",rf.me,rf.basicIndex,i,prevLogIndex)
					//TODO: SendSnapshot
				}
				var prevLogTerm int
				if prevLogIndex == rf.basicIndex{
					DPrintf("[rf.SendHeartBeat] leader=%v, basicIndex=%v, receiver=%v, prevLogIndex=%v",rf.me,rf.basicIndex,i,prevLogIndex)
					//TODO: prevLogTerm = snapshot.Term
					prevLogTerm = rf.logEntries[prevLogIndex-rf.basicIndex].Term
				}else{
					prevLogTerm = rf.logEntries[prevLogIndex-rf.basicIndex].Term
				}
				var newEntries []LogEntry	// default:nil
				if prevLogIndex + 1<rf.basicIndex+len(rf.logEntries){
					newEntries = make([]LogEntry,rf.basicIndex+len(rf.logEntries)-prevLogIndex-1)
					copy(newEntries,rf.logEntries[prevLogIndex+1-rf.basicIndex:])
				}
				rf.mu.Unlock()

				go func(i int, currentTerm int, commitIndex int, prevLogIndex int, prevLogTerm int, newEntries []LogEntry){
					args := AppendEntriesArgs{
						Term		: currentTerm,
						LeaderID	: me,
						PrevLogIndex: prevLogIndex,
						PrevLogTerm	: prevLogTerm,
						Entries		: newEntries,
						LeaderCommit: commitIndex,
					}
					reply := AppendEntriesReply{}

					ok := false
					ok = rf.sendAppendEntries(i,&args,&reply)

					rf.mu.Lock()
					if rf.state!=LEADER{
						rf.mu.Unlock()
						return
					}

					if ok && !reply.Success{
						// if the current Term is less than reply Term
						if reply.Term > rf.currentTerm{
							rf.currentTerm = reply.Term		// 单增，只会在reply.Term更大的时候
							rf.votedFor = -1
							rf.lastHeartBeat = time.Now()
							rf.persist()
							rf.mu.Unlock()
							go rf.ConvertToFollower()
							return
						}

						// network error
						if reply.XTerm == 0 && reply.Len ==0{
							rf.mu.Unlock()
							return
						}

						// if the log isn't matched
						// 1. the log is too short
						if reply.XTerm == 0{
							rf.nextIndex[i] = reply.Len
							// 2. the conflictingTerm haven't in the previous leader's log
						} else if ok,pos := TermInLeaderLog(rf.logEntries[:args.PrevLogIndex-rf.basicIndex],reply.XTerm); !ok{
							rf.nextIndex[i] = reply.XIndex
							// 3. the conflictingTerm have appeared in the previous leader's log
						} else{
							rf.nextIndex[i] = pos+rf.basicIndex
						}
						rf.mu.Unlock()
						return
					}

					// log matched, update nextIndex in the length limit
					if ok && reply.Success{
						if rf.matchIndex[i] <= prevLogIndex{
							rf.matchIndex[i] = prevLogIndex
							if rf.nextIndex[i] < rf.basicIndex+len(rf.logEntries){
								rf.nextIndex[i] += 1
							}
						}

						maxN := getMedian(rf.matchIndex)

						for i:=maxN;i>rf.commitIndex;i--{
							if rf.logEntries[i-rf.basicIndex].Term==rf.currentTerm{
								// Update commitIndex
								rf.commitIndex = i

								DPrintf("[rf.CheckCommitted] %v committed from index %v to index %v, basicIndex=%v, len(logEntries)=%v \n", rf.me,rf.lastApplied+1,i,rf.basicIndex,len(rf.logEntries))
								for j:=rf.lastApplied+1; j <= i;j++{
									rf.applyChan<-ApplyMsg{true,rf.logEntries[j-rf.basicIndex].Command,j, false,nil,0,0}
								}
								rf.lastApplied = rf.commitIndex
								break
							}
						}
						rf.mu.Unlock()
						return
					}
					rf.mu.Unlock()
				}(i, currentTerm, commitIndex, prevLogIndex ,prevLogTerm, newEntries)
				i += 1
			}

		}
	}
}

// ConvertToFollower convert the raft server to follower state
// happens once the leader is outdated or the candidate lose the election
// 1. change the state to follower
// 2. reset the election clock
// 3. if from leader, send the done signal for sending heartbeat
func (rf *Raft) ConvertToFollower(){
	rf.mu.Lock()
	defer rf.mu.Unlock()
	DPrintf("[rf.ConvertToFollower] %v converts to follower\n", rf.me)

	// if already turned to follower, return
	if rf.state == FOLLOWER{
		return
	}

	if rf.state==LEADER{
		close(rf.stopLeaderAction)	// close channel to stop all leader actions
	}
	rf.state = FOLLOWER
}

// ConvertToLeader is to convert the raft server to leader state
// happens once win the election
// 1. change the state to leader(so the check election time out won't be effective)
// 2.(concurrent) send AppendEntries RPC to peers repeatedly during idle periods
// 3.(concurrent) send AppendEntries RPC to peers if nextIndex satisfied
// 4.(concurrent)(done by SendHeartBeat) if currentTerm is lower, convert to follower
// 5.(concurrent) periodically update commitIndex according to matchIndex[], killed by convert to follower
func (rf *Raft) ConvertToLeader(){
	rf.mu.Lock()
	if rf.state == LEADER{
		rf.mu.Unlock()
		return
	}

	DPrintf("[rf.ConvertToLeader] %v converts to leader\n", rf.me)
	rf.state = LEADER
	Done := make(chan bool,2)
	rf.stopLeaderAction = Done
	rf.nextIndex = make([]int,len(rf.peers),len(rf.peers))
	rf.matchIndex = make([]int,len(rf.peers),len(rf.peers))
	for i:=0;i<len(rf.peers);i++{
		rf.nextIndex[i] = rf.basicIndex+len(rf.logEntries)
		rf.matchIndex[i] = 0
	}
	rf.mu.Unlock()

	go rf.SendHeartBeat()
}


// ConvertToCandidate is to convert the raft server to candidate state
// happens once the election timed out
// 1. change state to candidate
// 2. reset election clock
// 3. increase current term and vote for itself
// 4. send request vote to peers
// 5a. exit and continue checking election time out, start another term then
// 5b. convert to leader
func (rf *Raft) ConvertToCandidate(){
	rf.mu.Lock()
	DPrintf("[rf.ConvertToCandidate] %v converts to candidate\n", rf.me)
	rf.state = CANDIDATE
	rf.lastHeartBeat = time.Now()
	rf.currentTerm += 1
	rf.votedFor = rf.me
	rf.votes = 1

	peerLen := len(rf.peers)
	me := rf.me
	currentTerm := rf.currentTerm
	logEntries := rf.logEntries
	basicIndex := rf.basicIndex
	rf.persist()
	rf.mu.Unlock()

	i := 0
	for i < peerLen{
		if i == me{
			i += 1
			continue
		}

		go func(i int){
			args := RequestVoteArgs{
				Term			: currentTerm,
				CandidateId		: me,
				LastLogIndex	: basicIndex + len(logEntries)-1,						// not commitIndex nor lastApplied,but the largest index
				LastLogTerm		: logEntries[len(logEntries)-1].Term,
			}
			reply := RequestVoteReply{}
			rf.sendRequestVote(i,&args,&reply)
			rf.mu.Lock()
			if reply.Term > rf.currentTerm{
				rf.currentTerm = reply.Term		// 单增，只会在reply.Term更大的时候
				rf.votedFor = -1
				rf.lastHeartBeat = time.Now()
				rf.persist()
				rf.mu.Unlock()
				go rf.ConvertToFollower()
				return
			}
			if reply.Term == rf.currentTerm && reply.VoteGranted{
				rf.votes += 1
				if rf.currentTerm==currentTerm && rf.state==CANDIDATE && rf.votes > len(rf.peers)/2{
					rf.mu.Unlock()
					go rf.ConvertToLeader()
					return
				}
				rf.mu.Unlock()
				return
			}
			rf.mu.Unlock()
		}(i)
		i += 1
	}
}


func (rf *Raft) CheckElectionTimeout(){
	rf.mu.Lock()
	if rf.state != LEADER {
		if time.Now().Sub(rf.lastHeartBeat) >= time.Duration(200 + rand.Intn(300)) * time.Millisecond {
			rf.mu.Unlock()
			go rf.ConvertToCandidate()
			return
		}
	}
	rf.mu.Unlock()
}

func (rf *Raft) Check(){
	for true{
		select{
		case <-rf.stopElectionCheck:
			return
		case <- time.After(time.Duration(200 + rand.Intn(300)) * time.Millisecond):
			go rf.CheckElectionTimeout()
		}
	}
}

func MatchPrevLog(entries []LogEntry, prevLogIndex int, prevLogTerm int)(success bool,XTerm int,XIndex int,Len int){
	if prevLogIndex >= len(entries){
		return false,0,0, len(entries)
	}

	if entries[prevLogIndex].Term != prevLogTerm{
		conflictingTerm := entries[prevLogIndex].Term
		var i int
		for i=0;i<prevLogIndex;i++{
			if entries[i].Term == conflictingTerm{
				break
			}
		}
		return false, conflictingTerm, i, len(entries)
	}
	return true, 0, 0, 0
}

func UpdateNewEntries(entries []LogEntry, newEntries []LogEntry, prevLogIndex int)[]LogEntry{
	if newEntries==nil || len(newEntries)==0{
		return entries
	}

	oldPointer := prevLogIndex+1
	newPointer := 0
	for oldPointer < len(entries) && newPointer < len(newEntries){
		if entries[oldPointer].Term == newEntries[newPointer].Term{
			oldPointer+=1
			newPointer+=1
			continue
		} else{
			entries = entries[:oldPointer]
			for newPointer < len(newEntries){
				entries = append(entries,newEntries[newPointer])
				newPointer+=1
			}
			return entries
		}
	}

	if oldPointer < len(entries){
		return entries
	}

	for newPointer < len(newEntries){
		entries = append(entries,newEntries[newPointer])
		newPointer+=1
	}

	return entries
}

func TermInLeaderLog(entries []LogEntry, term int)(ok bool, lastpos int){
	for i:=len(entries)-1;i>=0;i--{
		if entries[i].Term==term{
			return true, i
		}
	}
	return false, -1
}


// AppendEntries is the heartbeat msg or the appending entries msg
// 1. if the leader is outdated, return false
// 2. if prevLogIndex and prevLogTerm don't match, return false
// 3. delete conflict entries and append new entries
// 4. update commit index
// 5a. reset election clock
// 5b. if the server is behind the leader term, convert to follower
//
// 2：if prevLogIndex don't match, return:	conflictingTerm
//											the first index of conflictingTerm in server's log
//											len(server's log) (in case the prevLogIndex > len(server's log))
func (rf *Raft) AppendEntries(args *AppendEntriesArgs,reply *AppendEntriesReply){
	rf.mu.Lock()

	// false leader has lower term, return false
	if args.Term < rf.currentTerm{
		reply.Term = rf.currentTerm
		reply.Success = false
		rf.mu.Unlock()
		return
	}

	// the term of current server is behind the leader term, convert to follower, reset timer
	if args.Term > rf.currentTerm{
		rf.currentTerm = args.Term
		rf.votedFor = -1
		rf.lastHeartBeat = time.Now()
		rf.persist()
		rf.mu.Unlock()

		go rf.ConvertToFollower()

		rf.mu.Lock()
	} else {
		rf.lastHeartBeat = time.Now()
	}

	// prevLogIndex and prevLogTerm don't match, return false
	if success,XTerm,XIndex,Len := MatchPrevLog(rf.logEntries,args.PrevLogIndex-rf.basicIndex,args.PrevLogTerm);!success{
		reply.Term = rf.currentTerm
		reply.Success = false
		reply.XTerm = XTerm
		reply.XIndex = XIndex+rf.basicIndex
		reply.Len = Len+rf.basicIndex
		rf.mu.Unlock()
		return
	}

	rf.logEntries = UpdateNewEntries(rf.logEntries,args.Entries,args.PrevLogIndex-rf.basicIndex)
	rf.persist()

	if args.LeaderCommit > rf.commitIndex{
		if args.LeaderCommit < rf.basicIndex+len(rf.logEntries)-1{
			rf.commitIndex = args.LeaderCommit
		}else {
			rf.commitIndex = rf.basicIndex+len(rf.logEntries)-1
		}
		DPrintf("[rf.AppendEntries] %v committed from index %v to index %v, len(logEntries)=%v basicIndex=%v \n", rf.me,rf.lastApplied+1,rf.commitIndex,len(rf.logEntries),rf.basicIndex)
		for i:=rf.lastApplied+1; i <= rf.commitIndex;i++{
			rf.applyChan<-ApplyMsg{true,rf.logEntries[i-rf.basicIndex].Command,i, false, nil, 0, 0}
		}
		rf.lastApplied = rf.commitIndex
	}

	reply.Term = rf.currentTerm
	reply.Success = true
	rf.mu.Unlock()
	return
}

// sendAppendEntries call appendEntries of the given server
// return bool representing of success or not
func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term 			int
	CandidateId		int
	LastLogIndex	int
	LastLogTerm		int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term			int
	VoteGranted		bool
}

//
// example RequestVote RPC handler.
// 1. if the leader is outdated return false
// 2. if the leader have already voted and votedFor != args.candidateId return false
// 3. grant vote if the candidate log is more or equally up-to-date
// 4. reset timer if vote granted
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()

	// 如果leader处于更高term,convert to follower并且立即更新term
	// 但是需要通过后续的检查才能vote
	if args.Term > rf.currentTerm{
		rf.currentTerm = args.Term
		rf.votedFor = -1
		rf.persist()
		rf.mu.Unlock()

		go rf.ConvertToFollower()
		rf.mu.Lock()
	}

	if args.Term < rf.currentTerm{
		reply.VoteGranted = false
		reply.Term = rf.currentTerm
		rf.mu.Unlock()
		return
	}

	if rf.votedFor!=-1 && rf.votedFor != args.CandidateId{
		reply.VoteGranted = false
		reply.Term = rf.currentTerm
		rf.mu.Unlock()
		return
	}

	// grant vote if the candidate's log more up-to-date
	lastIndex := rf.basicIndex+len(rf.logEntries)-1
	lastTerm := rf.logEntries[lastIndex-rf.basicIndex].Term
	if args.LastLogTerm > lastTerm || (args.LastLogTerm == lastTerm && args.LastLogIndex >= lastIndex){
		rf.lastHeartBeat = time.Now()
		rf.votedFor = args.CandidateId
		reply.VoteGranted = true
		reply.Term = rf.currentTerm
		rf.persist()
		rf.mu.Unlock()
		return
	}

	reply.VoteGranted = false
	reply.Term = rf.currentTerm
	rf.mu.Unlock()
	return
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}


//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := 0

	// Your code here (2B).
	// if current server isn't leader, return false
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.state!=LEADER{
		return index,rf.currentTerm,false
	}

	rf.logEntries = append(rf.logEntries,LogEntry{
		Term: rf.currentTerm,
		Command: command,
	})
	index = rf.basicIndex+len(rf.logEntries)-1
	rf.persist()

	return index, rf.currentTerm, true
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
	close(rf.stopElectionCheck)
	rf.mu.Lock()
	if rf.state==LEADER{
		close(rf.stopLeaderAction)
	}
	rf.mu.Unlock()
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
func (rf *Raft) ticker() {
	for rf.killed() == false {
		select{
		case <-rf.stopElectionCheck:
			return
		case <- time.After(time.Duration(200 + rand.Intn(300)) * time.Millisecond):
			go rf.CheckElectionTimeout()
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
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.mu.Lock()
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.currentTerm = 0
	rf.votedFor = -1	// null
	rf.votes = 0
	rf.commitIndex = 0
	rf.lastApplied = 0
	rf.logEntries = make([]LogEntry,0,5)
	rf.logEntries = append(rf.logEntries,LogEntry{0,nil})	// default value, not used, initial index = 1
	rf.applyChan = applyCh
	rf.lastHeartBeat = time.Now()	// so that every server is initialized with a different time
	rf.stopElectionCheck = make(chan int)
	rf.basicIndex = 0

	rf.state = FOLLOWER
	rf.mu.Unlock()

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	go rf.ticker()

	return rf
}

func getMedian(arr []int)int{
	arrCopy := make([]int,len(arr))
	for i,_ := range arrCopy{
		arrCopy[i] = arr[i]
	}
	sort.Ints(arrCopy)
	return arrCopy[(len(arr)-1)/2]
}

