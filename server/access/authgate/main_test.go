package authgate

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"minecraft-server/server/access/auth"
)

// TestMain hashes passwords at bcrypt's minimum cost: the tests time
// register / login against short deadlines, which a cost-10 hash under the
// race detector in a full parallel run can miss.
func TestMain(m *testing.M) {
	auth.HashCost = bcrypt.MinCost
	os.Exit(m.Run())
}
