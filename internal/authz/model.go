package authz

// Model is the Casbin model for the v2 RBAC system described in
// CONTRACT.md §13 v2.
//
// Request shape:
//
//	r = sub_role, sub_id, obj, act
//
//	sub_role  the role being evaluated (one Enforce call per user role)
//	sub_id    the user's UUID as a string, used by the "own" qualifier
//	obj       *Resource — Type, ID, Visibility, AuthorID
//	act       the requested action: "view" | "create" | "edit" | "delete"
//
// Policy shape:
//
//	p = sub, obj_type, obj_instance, act_set
//
//	sub            the role granted by the policy
//	obj_type       "*" or one of the resource types from §13 v2
//	obj_instance   "*" | "public" | "unlisted" | "own" | "shared" | <uuid>
//	act_set        "*" or a comma-separated subset of view,create,edit,delete
//
// Two custom matcher functions, registered at enforcer construction time,
// handle the action-set membership test and the instance-qualifier
// resolution. The "shared" qualifier delegates to a SharesLookup that
// queries the shares table introduced by mi-1mv.
const Model = `
[request_definition]
r = sub_role, sub_id, obj, act

[policy_definition]
p = sub, obj_type, obj_instance, act_set

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub_role, p.sub) && (p.obj_type == "*" || r.obj.Type == p.obj_type) && actionMatch(r.act, p.act_set) && instanceMatch(p.obj_instance, r.sub_id, r.obj)
`
