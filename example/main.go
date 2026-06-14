package main

import (
	"errors"
	"fmt"

	"github.com/yumosx/depends/di"
)

// ---------- 领域类型 ----------

// AsyncSession 是底层连接，等价于 Python 的 AsyncSession。
type AsyncSession struct {
	ID int
}

// UserDao / RoleRepository / UserRepository / UserService 与 Python 一一对应。
type UserDao struct {
	Session *AsyncSession
}

type RoleRepository struct{}

type UserRepository struct {
	dao *UserDao
}

type UserService struct {
	repo     *UserRepository
	roleRepo *RoleRepository
}

// ---------- provider 函数 ----------
//
// 注意这里只有「普通函数 + 类型化的形参」，
// 没有任何 DI 相关的语法——和 Python 里 def 一样干净。

func getUserDao(session *AsyncSession) *UserDao {
	return &UserDao{Session: session}
}

func getUserRepository(dao *UserDao) *UserRepository {
	return &UserRepository{dao: dao}
}

func getUserService(repo *UserRepository, roleRepo *RoleRepository) *UserService {
	return &UserService{repo: repo, roleRepo: roleRepo}
}

func getAsyncSession() *AsyncSession {
	return &AsyncSession{ID: 42}
}

func getRoleRepository() (*RoleRepository, error) {
	return &RoleRepository{}, nil
}

func main() {
	c := di.New()

	di.Depends[*AsyncSession](c, getAsyncSession)
	di.Depends[*UserDao](c, getUserDao)
	di.Depends[*RoleRepository](c, getRoleRepository)
	di.Depends[*UserRepository](c, getUserRepository)
	UserServiceDep := di.Depends[*UserService](c, getUserService)

	// 整个调用链：UserService -> UserRepository -> UserDao -> AsyncSession，
	svc, err := UserServiceDep.Get()
	if err != nil {
		var notFound *di.NotFoundError
		if errors.As(err, &notFound) {
			fmt.Println("missing dep:", notFound)
		}
		panic(err)
	}
	fmt.Printf("UserService: %+v\n", svc)
	fmt.Printf("  repo.dao.Session.ID = %d\n", svc.repo.dao.Session.ID)
	fmt.Printf("  roleRepo            = %+v\n", svc.roleRepo)

	// 验证单例：再次解析拿到的是同一个指针。
	svc2 := UserServiceDep.MustGet()
	fmt.Printf("singleton? %v\n", svc == svc2)
}
