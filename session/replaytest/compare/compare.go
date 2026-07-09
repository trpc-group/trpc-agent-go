package compare

import "trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"

// 比较两个切片是否相同  类似深拷贝？？
// @ todo 是不是可以设置优先级？ 对于右优先级低的部分 就算是不同也算作是成功的？ 这个微小的不同当做是误差
func CompareDeep(a, b *normalize.SnapShot) bool {
	// true means the same
	return len(MakeDiff(a, b)) == 0
}
