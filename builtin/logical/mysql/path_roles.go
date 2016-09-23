package mysql

import (
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/hashicorp/vault/helper/strutil"
	"github.com/hashicorp/vault/logical"
	"github.com/hashicorp/vault/logical/framework"
)

func pathListRoles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/?$",

		Callbacks: map[logical.Operation]framework.OperationFunc{
			logical.ListOperation: b.pathRoleList,
		},

		HelpSynopsis:    pathRoleHelpSyn,
		HelpDescription: pathRoleHelpDesc,
	}
}

func pathRoles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/" + framework.GenericNameRegex("name"),
		Fields: map[string]*framework.FieldSchema{
			"name": &framework.FieldSchema{
				Type:        framework.TypeString,
				Description: "Name of the role.",
			},

			"sql": &framework.FieldSchema{
				Type:        framework.TypeString,
				Description: "SQL string to create a user. See help for more info.",
			},

			"revoke_sql": &framework.FieldSchema{
				Type:        framework.TypeString,
				Description: "SQL string to revoke a user. See help for more info.",
			},

			"username_length": &framework.FieldSchema{
				Type:        framework.TypeInt,
				Description: "number of characters to truncate generated mysql usernames to (default 16)",
				Default:     16,
			},

			"rolename_length": &framework.FieldSchema{
				Type:        framework.TypeInt,
				Description: "number of characters to truncate the rolename portion of generated mysql usernames to (default 4)",
				Default:     4,
			},

			"displayname_length": &framework.FieldSchema{
				Type:        framework.TypeInt,
				Description: "number of characters to truncate the displayname portion of generated mysql usernames to (default 4)",
				Default:     4,
			},
		},

		Callbacks: map[logical.Operation]framework.OperationFunc{
			logical.ReadOperation:   b.pathRoleRead,
			logical.UpdateOperation: b.pathRoleCreate,
			logical.DeleteOperation: b.pathRoleDelete,
		},

		HelpSynopsis:    pathRoleHelpSyn,
		HelpDescription: pathRoleHelpDesc,
	}
}

func (b *backend) Role(s logical.Storage, n string) (*roleEntry, error) {
	entry, err := s.Get("role/" + n)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	// Set defaults to handle upgrade cases
	result := roleEntry{
		UsernameLength:    16,
		RolenameLength:    4,
		DisplaynameLength: 4,
	}

	if err := entry.DecodeJSON(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (b *backend) pathRoleDelete(
	req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	err := req.Storage.Delete("role/" + data.Get("name").(string))
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (b *backend) pathRoleRead(
	req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	role, err := b.Role(req.Storage, data.Get("name").(string))
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, nil
	}

	return &logical.Response{
		Data: map[string]interface{}{
			"sql":        role.SQL,
			"revoke_sql": role.RevokeSQL,
		},
	}, nil
}

func (b *backend) pathRoleList(
	req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	entries, err := req.Storage.List("role/")
	if err != nil {
		return nil, err
	}

	return logical.ListResponse(entries), nil
}

func (b *backend) pathRoleCreate(
	req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	sql := data.Get("sql").(string)
	revoke_sql := data.Get("revoke_sql").(string)
	username_length := data.Get("username_length").(int)
	rolename_length := data.Get("rolename_length").(int)
	displayname_length := data.Get("displayname_length").(int)

	// Get our connection
	db, err := b.DB(req.Storage)
	if err != nil {
		return nil, err
	}

	// Test the query by trying to prepare it
	for _, query := range strutil.ParseArbitraryStringSlice(sql, ";") {
		query = strings.TrimSpace(query)
		if len(query) == 0 {
			continue
		}

		stmt, err := db.Prepare(Query(query, map[string]string{
			"name":     "foo",
			"password": "bar",
		}))
		if err != nil {
			return logical.ErrorResponse(fmt.Sprintf(
				"Error testing query: %s", err)), nil
		}
		stmt.Close()
	}

	// Store it
	entry, err := logical.StorageEntryJSON("role/"+name, &roleEntry{
		SQL:               sql,
		RevokeSQL:         revoke_sql,
		UsernameLength:    username_length,
		DisplaynameLength: displayname_length,
		RolenameLength:    rolename_length,
	})
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(entry); err != nil {
		return nil, err
	}
	return nil, nil
}

type roleEntry struct {
	SQL               string `json:"sql"`
	RevokeSQL         string `json:"revoke_sql"`
	UsernameLength    int    `json:"username_length"`
	DisplaynameLength int    `json:"displayname_length"`
	RolenameLength    int    `json:"rolename_length"`
}

const pathRoleHelpSyn = `
Manage the roles that can be created with this backend.
`

const pathRoleHelpDesc = `
This path lets you manage the roles that can be created with this backend.

The "sql" parameter customizes the SQL string used to create the role.
This can be a sequence of SQL queries, each semi-colon seperated. Some
substitution will be done to the SQL string for certain keys.
The names of the variables must be surrounded by "{{" and "}}" to be replaced.

  * "name" - The random username generated for the DB user.

  * "password" - The random password generated for the DB user.

Example of a decent SQL query to use:

  CREATE USER '{{name}}'@'%' IDENTIFIED BY '{{password}}';
  GRANT ALL ON db1.* TO '{{name}}'@'%';

Note the above user would be able to access anything in db1. Please see the MySQL
manual on the GRANT command to learn how to do more fine grained access.

The "rolename_length" parameter determines how many characters of the role name
will be used in creating the generated mysql username; the default is 4.

The "displayname_length" parameter determines how many characters of the token
display name will be used in creating the generated mysql username; the default
is 4.

The "username_length" parameter determines how many total characters the
generated username (including the role name, token display name and the uuid
portion) will be truncated to.  Versions of MySQL prior to 5.7.8 are limited to
16 characters total (see
http://dev.mysql.com/doc/refman/5.7/en/user-names.html) so that is the default;
for versions >=5.7.8 it is safe to increase this to 32.

For best readability in MySQL process lists, we recommend using MySQL 5.7.8 or
later, setting "username_length" to 32 and setting both "rolename_length" and
"displayname_length" to 8.  However due the the prevalence of older versions of
MySQL in general deployment, the defaults are currently tuned for a
username_length of 16.
`
