// Job Object 辅助：创建带 KILL_ON_JOB_CLOSE 的 Job，把子进程加入其中。
// 这样无论启动器正常退出还是被强制结束（Task Manager），Job 句柄关闭时
// Windows 都会终止其中所有进程，保证不留孤儿 node 进程。
package win

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	processTerminate = 0x0001
	processSetQuota  = 0x0100

	jobObjectLimitKillOnJobClose = 0x2000

	jobObjectExtendedLimitInformationClass = 9
)

// 与 Windows SDK 定义一致的布局（64 位）。
type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateJobObjectW        = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob      = kernel32.NewProc("AssignProcessToJobObject")
	procOpenProcess             = kernel32.NewProc("OpenProcess")
	procCloseHandle             = kernel32.NewProc("CloseHandle")
)

// Job 是一个 Windows Job Object，句柄关闭时结束其中所有进程。
type Job struct {
	handle syscall.Handle
}

// NewKillOnCloseJob 创建带 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 的 Job Object。
func NewKillOnCloseJob() (*Job, error) {
	r, _, err := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return nil, fmt.Errorf("CreateJobObjectW 失败：%v", err)
	}
	return &Job{handle: syscall.Handle(r)}, nil
}

// AssignPID 把 pid 对应的进程加入 Job。
func (j *Job) AssignPID(pid uint32) error {
	// 先设置 KILL_ON_JOB_CLOSE。
	info := jobObjectExtendedLimitInformation{
		BasicLimitInformation: jobObjectBasicLimitInformation{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}
	r, _, err := procSetInformationJobObject.Call(
		uintptr(j.handle),
		jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r == 0 {
		return fmt.Errorf("SetInformationJobObject 失败：%v", err)
	}

	// AssignProcessToJobObject 需要 PROCESS_SET_QUOTA | PROCESS_TERMINATE。
	h, _, err := procOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(pid))
	if h == 0 {
		return fmt.Errorf("OpenProcess(%d) 失败：%v", pid, err)
	}
	defer procCloseHandle.Call(h)

	r, _, err = procAssignProcessToJob.Call(uintptr(j.handle), h)
	if r == 0 {
		return fmt.Errorf("AssignProcessToJobObject(%d) 失败：%v", pid, err)
	}
	return nil
}

// Close 关闭句柄（触发 KILL_ON_JOB_CLOSE，结束 Job 内所有进程）。
func (j *Job) Close() {
	if j.handle != 0 {
		_, _, _ = procCloseHandle.Call(uintptr(j.handle))
		j.handle = 0
	}
}
