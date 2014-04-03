/* Node object/class/whatever it is that Go calls them. */

/*
 * Copyright (c) 2013-2014, Jeremy Bingham (<jbingham@gmail.com>)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package node implements nodes. They do chef-client runs.
package node

import (
	"github.com/ctdk/goiardi/config"
	"github.com/ctdk/goiardi/data_store"
	"github.com/ctdk/goiardi/util"
	"github.com/ctdk/goiardi/indexer"
	"fmt"
	"net/http"
	"log"
	"database/sql"
)

type Node struct {
	Name string `json:"name"`
	ChefEnvironment string `json:"chef_environment"`
	RunList []string `json:"run_list"`
	JsonClass string `json:"json_class"`
	ChefType string `json:"chef_type"`
	Automatic map[string]interface{} `json:"automatic"`
	Normal map[string]interface{} `json:"normal"`
	Default map[string]interface{} `json:"default"`
	Override map[string]interface{} `json:"override"`
}

func New(name string) (*Node, util.Gerror) {
	/* check for an existing node with this name */
	if config.Config.UseMySQL {
		// will need redone if orgs ever get implemented
		_, err := data_store.CheckForOne(data_store.Dbh, "nodes", name)
		if err == nil {
			gerr := util.Errorf("Node %s already exists", name)
			gerr.SetStatus(http.StatusConflict)
			return nil, gerr
		} else {
			if err != sql.ErrNoRows {
				gerr := util.Errorf(err.Error())
				gerr.SetStatus(http.StatusInternalServerError)
				return nil, gerr
			}
		}
	} else {
		ds := data_store.New()
		if _, found := ds.Get("node", name); found {
			err := util.Errorf("Node %s already exists", name)
			err.SetStatus(http.StatusConflict)
			return nil, err
		}
	}
	if !util.ValidateDBagName(name){
		err := util.Errorf("Field 'name' invalid")
		return nil, err
	}
	/* No node, we make a new one */
	node := &Node{
		Name: name,
		ChefEnvironment: "_default",
		ChefType: "node",
		JsonClass: "Chef::Node",
		RunList: []string{},
		Automatic: map[string]interface{}{},
		Normal: map[string]interface{}{},
		Default: map[string]interface{}{},
		Override: map[string]interface{}{},
	}
	return node, nil
}

// Create a new node from the uploaded JSON.
func NewFromJson(json_node map[string]interface{}) (*Node, util.Gerror){
	node_name, nerr := util.ValidateAsString(json_node["name"])
	if nerr != nil {
		return nil, nerr
	}
	node, err := New(node_name)
	if err != nil {
		return nil, err
	}
	err = node.UpdateFromJson(json_node)
	if err != nil {
		return nil, err
	}
	return node, nil
}

// Fill in a node from a row returned from the SQL server. Useful for the case
// down the road where an array of objects is needed, but building it with
// a call to GetList(), then repeated calls to Get() sucks with a real db even
// if it's marginally acceptable in in-memory mode.
//
// NB: This does require the query to look like the one in Get().
func (n *Node) fillNodeFromSQL(row *sql.Row) error {
	if config.Config.UseMySQL {
		var (
			rl []byte
			aa []byte
			na []byte
			da []byte
			oa []byte
		)
		err := row.Scan(&n.Name, &n.ChefEnvironment, &rl, &aa, &na, &da, &oa)
		if err != nil {
			return err
		}
		n.ChefType = "node"
		n.JsonClass = "Chef::Node"
		var q interface{}
		q, err = data_store.DecodeBlob(rl, n.RunList)
		if err != nil {
			return err
		}
		n.RunList = q.([]string)
		q, err = data_store.DecodeBlob(aa, n.Automatic)
		if err != nil {
			return err
		}
		n.Automatic = q.(map[string]interface{})
		q, err = data_store.DecodeBlob(na, n.Normal)
		if err != nil {
			return err
		}
		n.Normal = q.(map[string]interface{})
		q, err = data_store.DecodeBlob(da, n.Default)
		if err != nil {
			return err
		}
		n.Default = q.(map[string]interface{})
		q, err = data_store.DecodeBlob(oa, n.Override)
		if err != nil {
			return err
		}
		n.Override = q.(map[string]interface{})
		data_store.ChkNilArray(n)
	} else { // add Postgres later
		err := fmt.Errorf("no database configured, operating in in-memory mode -- fillNodeFromSQL cannot be run")
		return err
	}
	return nil
}

func Get(node_name string) (*Node, error) {
	var node *Node
	var found bool
	if config.Config.UseMySQL {
		node = new(Node)
		stmt, err := data_store.Dbh.Prepare("select n.name, e.name as chef_environment, n.run_list, n.automatic_attr, n.normal_attr, n.default_attr, n.override_attr from nodes n join environments as e on n.environment_id = e.id where n.name = ?")
		if err != nil {
			return nil, err
		}
		defer stmt.Close()
		row := stmt.QueryRow(node_name)
		err = node.fillNodeFromSQL(row)

		if err != nil {
			if err == sql.ErrNoRows {
				found = false
			} else {
				return nil, err
			}
		} else {
			found = true
		}
	} else {
		ds := data_store.New()
		var n interface{}
		n, found = ds.Get("node", node_name)
		node = n.(*Node)
	}
	if !found {
		err := fmt.Errorf("node '%s' not found", node_name)
		return nil, err
	}
	return node, nil
}

// Update an existing node with the uploaded JSON.
func (n *Node) UpdateFromJson(json_node map[string]interface{}) util.Gerror {
	/* It's actually totally legitimate to save a node with a different
	 * name than you started with, but we need to get/create a new node for
	 * it is all. */
	node_name, nerr := util.ValidateAsString(json_node["name"])
	if nerr != nil {
		return nerr
	}
	if n.Name != node_name {
		err := util.Errorf("Node name %s and %s from JSON do not match.", n.Name, node_name)
		return err
	}

	/* Validations */

	/* Look for invalid top level elements. *We* don't have to worry about
	 * them, but chef-pedant cares (probably because Chef <=10 stores
 	 * json objects directly, dunno about Chef 11). */
	valid_elements := []string{ "name", "json_class", "chef_type", "chef_environment", "run_list", "override", "normal", "default", "automatic" }
	ValidElem:
	for k, _ := range json_node {
		for _, i := range valid_elements {
			if k == i {
				continue ValidElem
			}
		}
		err := util.Errorf("Invalid key %s in request body", k)
		return err
	}

	var verr util.Gerror
	json_node["run_list"], verr = util.ValidateRunList(json_node["run_list"])
	if verr != nil {
		return verr
	}
	attrs := []string{ "normal", "automatic", "default", "override" }
	for _, a := range attrs {
		json_node[a], verr = util.ValidateAttributes(a, json_node[a])
		if verr != nil {
			return verr
		}
	}

	json_node["chef_environment"], verr = util.ValidateAsFieldString(json_node["chef_environment"])
	if verr != nil {
		if verr.Error() == "Field 'name' nil" {
			json_node["chef_environment"] = n.ChefEnvironment
		} else {
			return verr
		}
	} else {
		if !util.ValidateEnvName(json_node["chef_environment"].(string)) {
			verr = util.Errorf("Field 'chef_environment' invalid")
			return verr
		}
	}

	json_node["json_class"], verr = util.ValidateAsFieldString(json_node["json_class"])
	if verr != nil {
		if verr.Error() == "Field 'name' nil" {
			json_node["json_class"] = n.JsonClass
		} else {
			return verr
		}
	} else {
		if json_node["json_class"].(string) != "Chef::Node" {
			verr = util.Errorf("Field 'json_class' invalid")
			return verr
		}
	}


	json_node["chef_type"], verr = util.ValidateAsFieldString(json_node["chef_type"])
	if verr != nil {
		if verr.Error() == "Field 'name' nil" {
			json_node["chef_type"] = n.ChefType
		} else {
			return verr
		}
	} else {
		if json_node["chef_type"].(string) != "node" {
			verr = util.Errorf("Field 'chef_type' invalid")
			return verr
		}
	}

	/* and setting */
	n.ChefEnvironment = json_node["chef_environment"].(string)
	n.ChefType = json_node["chef_type"].(string)
	n.JsonClass = json_node["json_class"].(string)
	n.RunList = json_node["run_list"].([]string)
	n.Normal = json_node["normal"].(map[string]interface{})
	n.Automatic = json_node["automatic"].(map[string]interface{})
	n.Default = json_node["default"].(map[string]interface{})
	n.Override = json_node["override"].(map[string]interface{})
	return nil
}

func (n *Node) Save() error {
	if config.Config.UseMySQL {
		// prepare the complex structures for saving
		rlb, rlerr := data_store.EncodeBlob(n.RunList)
		if rlerr != nil {
			return rlerr
		}
		aab, aaerr := data_store.EncodeBlob(n.Automatic)
		if aaerr != nil {
			return aaerr
		}
		nab, naerr := data_store.EncodeBlob(n.Normal)
		if naerr != nil {
			return naerr
		}
		dab, daerr := data_store.EncodeBlob(n.Default)
		if daerr != nil {
			return daerr
		}
		oab, oaerr := data_store.EncodeBlob(n.Override)
		if oaerr != nil {
			return oaerr
		}

		tx, err := data_store.Dbh.Begin()
		var node_id int32
		if err != nil {
			return err
		}
		// This does not use the INSERT ... ON DUPLICATE KEY UPDATE
		// syntax to keep the MySQL code & the future Postgres code
		// closer together.
		node_id, err = data_store.CheckForOne(tx, "nodes", n.Name)
		if err == nil {
			// probably want binlog_format set to MIXED or ROW for 
			// this query
			_, err := tx.Exec("UPDATE nodes n, environments e SET n.environment_id = e.id, n.run_list = ?, n.automatic_attr = ?, n.normal_attr = ?, n.default_attr = ?, n.override_attr = ?, n.updated_at = NOW() WHERE n.id = ? and e.name = ?", rlb, aab, nab, dab, oab, node_id, n.ChefEnvironment)
			if err != nil {
				tx.Rollback()
				return err
			}
		} else {
			if err != sql.ErrNoRows {
				tx.Rollback()
				return err
			}
			var environment_id int32
			environment_id, err = data_store.CheckForOne(tx, "environments", n.ChefEnvironment)
			if err != nil {
				tx.Rollback()
				return err
			}
			_, err = tx.Exec("INSERT INTO nodes (name, environment_id, run_list, automatic_attr, normal_attr, default_attr, override_attr, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, NOW(), NOW())", n.Name, environment_id, rlb, aab, nab, dab, oab)
			if err != nil {
				tx.Rollback()
				return err
			}
		}
		tx.Commit()
	} else {
		ds := data_store.New()
		ds.Set("node", n.Name, n)
	}
	/* TODO Later: excellent candidate for a goroutine */
	indexer.IndexObj(n)
	return nil
}

func (n *Node) Delete() error {
	if config.Config.UseMySQL {
		tx, err := data_store.Dbh.Begin()
		if err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM nodes WHERE name = ?", n.Name)
		if err != nil {
			terr := tx.Rollback()
			if terr != nil {
				err = fmt.Errorf("deleting node %s had an error '%s', and then rolling back the transaction gave another error '%s'", n.Name, err.Error(), terr.Error())
			}
			return err
		}
		tx.Commit()
	} else {
		ds := data_store.New()
		ds.Delete("node", n.Name)
	}
	indexer.DeleteItemFromCollection("node", n.Name)
	return nil
}

// Get a list of the nodes on this server.
func GetList() []string {
	var node_list []string
	if config.Config.UseMySQL {
		rows, err := data_store.Dbh.Query("SELECT name FROM nodes")
		if err != nil {
			if err != sql.ErrNoRows {
				log.Fatal(err)
			}
			rows.Close()
			return node_list
		}
		node_list = make([]string, 0)
		for rows.Next() {
			var node_name string
			err = rows.Scan(&node_name)
			if err != nil {
				log.Fatal(err)
			}
			node_list = append(node_list, node_name)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			log.Fatal(err)
		}
	} else {
		ds := data_store.New()
		node_list = ds.GetList("node")
	}
	return node_list
}

func (n *Node) GetName() string {
	return n.Name
}

func (n *Node) URLType() string {
	return "nodes"
}

/* Functions to support indexing */

func (n *Node) DocId() string {
	return n.Name
}

func (n *Node) Index() string {
	return "node"
}

func (n *Node) Flatten() []string {
	flatten := util.FlattenObj(n)
	indexified := util.Indexify(flatten)
	return indexified
}