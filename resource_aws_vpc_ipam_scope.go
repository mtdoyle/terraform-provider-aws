package aws

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/aws-sdk-go-base/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceAwsVpcIpamScope() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsVpcIpamScopeCreate,
		Read:   resourceAwsVpcIpamScopeRead,
		Update: resourceAwsVpcIpamScopeUpdate,
		Delete: resourceAwsVpcIpamScopeDelete,
		// CustomizeDiff: customdiff.Sequence(SetTagsDiff),
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"description": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"ipam_arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"ipam_id": {
				Type:     schema.TypeString,
				Required: true,
			},
			"ipam_scope_type": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"is_default": {
				Type:     schema.TypeBool,
				Computed: true,
				ForceNew: true,
			},
			"pool_count": {
				Type:     schema.TypeInt,
				Computed: true,
				ForceNew: true,
			},
			// "tags":     tagsSchema(),
			// "tags_all": tagsSchemaComputed(),
		},
	}
}

const (
	IpamScopeDeleteTimeout = 3 * time.Minute
	IpamScopeDeleteDelay   = 5 * time.Second

	IpamScopeStatusAvailable   = "Available"
	InvalidIpamScopeIdNotFound = "InvalidIpamScopeId.NotFound"
)

func resourceAwsVpcIpamScopeCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn
	// defaultTagsConfig := meta.(*AWSClient).DefaultTagsConfig
	// tags := defaultTagsConfig.MergeTags(keyvaluetags.New(d.Get("tags").(map[string]interface{})))

	input := &ec2.CreateIpamScopeInput{
		ClientToken: aws.String(resource.UniqueId()),
		IpamId:      aws.String(d.Get("ipam_id").(string)),
		// TagSpecifications: ec2TagSpecificationsFromKeyValueTags(tags, ec2.ResourceTypeVolume),
	}

	if v, ok := d.GetOk("description"); ok {
		input.Description = aws.String(v.(string))
	}

	log.Printf("[DEBUG] Creating IPAM Scope: %s", input)
	output, err := conn.CreateIpamScope(input)
	if err != nil {
		return fmt.Errorf("Error creating ipam scope in ipam (%s): %w", d.Get("ipam_id").(string), err)
	}
	d.SetId(aws.StringValue(output.IpamScope.IpamScopeId))
	log.Printf("[INFO] IPAM Scope ID: %s", d.Id())

	// if _, err = waiter.IpamScopeAvailable(conn, d.Id(), IpamScopeCreateTimeout); err != nil {
	// 	return fmt.Errorf("error waiting for IPAM Scope (%s) to be Available: %w", d.Id(), err)
	// }

	return resourceAwsVpcIpamScopeRead(d, meta)
}

func resourceAwsVpcIpamScopeRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn
	// defaultTagsConfig := meta.(*AWSClient).DefaultTagsConfig
	// tags := defaultTagsConfig.MergeTags(keyvaluetags.New(d.Get("tags").(map[string]interface{})))

	scope, err := findIpamScopeById(conn, d.Id())
	ipamId := strings.Split(*scope.IpamArn, "/")[1]

	if err != nil && !tfawserr.ErrCodeEquals(err, InvalidIpamScopeIdNotFound) {
		return err
	}

	if !d.IsNewResource() && scope == nil {
		log.Printf("[WARN] IPAM Scope (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	d.Set("arn", scope.IpamScopeArn)
	d.Set("description", scope.Description)
	d.Set("ipam_arn", scope.IpamArn)
	d.Set("ipam_id", ipamId)
	d.Set("ipam_scope_type", scope.IpamScopeType)
	d.Set("is_default", scope.IsDefault)
	d.Set("pool_count", scope.PoolCount)

	return nil
}

func resourceAwsVpcIpamScopeUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	// if d.HasChange("tags_all") {
	// 	o, n := d.GetChange("tags_all")
	// 	if err := keyvaluetags.StoragegatewayUpdateTags(conn, d.Get("arn").(string), o, n); err != nil {
	// 		return fmt.Errorf("error updating tags: %w", err)
	// 	}
	// }}

	input := &ec2.ModifyIpamScopeInput{
		IpamScopeId: aws.String(d.Id()),
	}

	if d.HasChangesExcept("tags_all") {
		if v, ok := d.GetOk("description"); ok {
			input.Description = aws.String(v.(string))
		}
	}
	log.Printf("[DEBUG] Updating IPAM scope: %s", input)
	_, err := conn.ModifyIpamScope(input)
	if err != nil {
		return fmt.Errorf("error updating IPAM Scope (%s): %w", d.Id(), err)
	}

	return resourceAwsVpcIpamScopeRead(d, meta)
}

func resourceAwsVpcIpamScopeDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	input := &ec2.DeleteIpamScopeInput{
		IpamScopeId: aws.String(d.Id()),
	}

	log.Printf("[DEBUG] Deleting IPAM Scope: %s", input)
	_, err := conn.DeleteIpamScope(input)
	if err != nil {
		return fmt.Errorf("error deleting IPAM Scope: (%s): %w", d.Id(), err)
	}

	if _, err = waitIpamScopeDeleted(conn, d.Id(), IpamScopeDeleteTimeout); err != nil {
		if isResourceNotFoundError(err) {
			return nil
		}
		return fmt.Errorf("error waiting for IPAM Scope (%s) to be deleted: %w", d.Id(), err)
	}

	return nil
}

func findIpamScopeById(conn *ec2.EC2, id string) (*ec2.IpamScope, error) {
	input := &ec2.DescribeIpamScopesInput{
		IpamScopeIds: aws.StringSlice([]string{id}),
	}

	output, err := conn.DescribeIpamScopes(input)

	if err != nil {
		return nil, err
	}

	if output == nil || len(output.IpamScopes) == 0 || output.IpamScopes[0] == nil {
		return nil, nil
	}

	return output.IpamScopes[0], nil
}

func waitIpamScopeDeleted(conn *ec2.EC2, ipamScopeId string, timeout time.Duration) (*ec2.IpamScope, error) {
	stateConf := &resource.StateChangeConf{
		Pending: []string{IpamScopeStatusAvailable},
		Target:  []string{InvalidIpamScopeIdNotFound},
		Refresh: statusIpamScopeStatus(conn, ipamScopeId),
		Timeout: timeout,
		Delay:   IpamScopeDeleteDelay,
	}

	outputRaw, err := stateConf.WaitForState()

	if output, ok := outputRaw.(*ec2.IpamScope); ok {
		return output, err
	}

	return nil, err
}

func statusIpamScopeStatus(conn *ec2.EC2, ipamScopeId string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {

		output, err := findIpamScopeById(conn, ipamScopeId)

		if tfawserr.ErrCodeEquals(err, InvalidIpamScopeIdNotFound) {
			return output, InvalidIpamScopeIdNotFound, nil
		}

		// there was an unhandled error in the Finder
		if err != nil {
			return nil, "", err
		}

		return output, IpamScopeStatusAvailable, nil
	}
}
