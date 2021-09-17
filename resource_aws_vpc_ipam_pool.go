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
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/keyvaluetags"
)

func resourceAwsVpcIpamPool() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsVpcIpamPoolCreate,
		Read:   resourceAwsVpcIpamPoolRead,
		Update: resourceAwsVpcIpamPoolUpdate,
		Delete: resourceAwsVpcIpamPoolDelete,
		// CustomizeDiff: customdiff.Sequence(SetTagsDiff),
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"address_family": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(ec2.AddressFamily_Values(), false),
			},
			"advertisable": {
				Type:     schema.TypeBool,
				Optional: true,
			},
			"allocation_default_netmask_length": {
				Type:         schema.TypeInt,
				Optional:     true,
				ValidateFunc: validation.IntBetween(0, 128),
			},
			"allocation_max_netmask_length": {
				Type:         schema.TypeInt,
				Optional:     true,
				ValidateFunc: validation.IntBetween(0, 128),
			},
			"allocation_min_netmask_length": {
				Type:         schema.TypeInt,
				Optional:     true,
				ValidateFunc: validation.IntBetween(0, 128),
			},
			"allocation_resource_tags": tagsSchema(),
			"auto_import": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"description": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"ipam_scope_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"ipam_scope_type": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"locale": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				ValidateFunc: validation.Any(
					validation.StringInSlice([]string{"None"}, false),
					validateRegionName,
				),
				Default: "None",
			},
			"pool_depth": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"source_ipam_pool_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"state": {
				Type:     schema.TypeString,
				Computed: true,
			},
			// "tags":     tagsSchema(),
			// "tags_all": tagsSchemaComputed(),
		},
	}
}

const (
	IpamPoolCreateTimeout     = 3 * time.Minute
	InvalidIpamPoolIdNotFound = "InvalidIpamPoolId.NotFound"
	IpamPoolUpdateTimeout     = 3 * time.Minute
	IpamPoolDeleteTimeout     = 3 * time.Minute
	IpamPoolAvailableDelay    = 5 * time.Second
	IpamPoolDeleteDelay       = 5 * time.Second
)

func resourceAwsVpcIpamPoolCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn
	// defaultTagsConfig := meta.(*AWSClient).DefaultTagsConfig
	// tags := defaultTagsConfig.MergeTags(keyvaluetags.New(d.Get("tags").(map[string]interface{})))

	input := &ec2.CreateIpamPoolInput{
		AddressFamily: aws.String(d.Get("address_family").(string)),
		ClientToken:   aws.String(resource.UniqueId()),
		IpamScopeId:   aws.String(d.Get("ipam_scope_id").(string)),
		// TagSpecifications: ec2TagSpecificationsFromKeyValueTags(tags, ec2.ResourceTypeVolume),
	}

	if v := d.Get("advertisable"); v != "" && d.Get("address_family") == ec2.AddressFamilyIpv6 {
		input.Advertisable = aws.Bool(v.(bool))
	}

	if v, ok := d.GetOk("allocation_default_netmask_length"); ok {
		input.AllocationDefaultNetmaskLength = aws.Int64(int64(v.(int)))
	}

	if v, ok := d.GetOk("allocation_max_netmask_length"); ok {
		input.AllocationMaxNetmaskLength = aws.Int64(int64(v.(int)))
	}

	if v, ok := d.GetOk("allocation_min_netmask_length"); ok {
		input.AllocationMinNetmaskLength = aws.Int64(int64(v.(int)))
	}

	if v, ok := d.GetOk("allocation_resource_tags"); ok && len(v.(map[string]interface{})) > 0 {
		input.AllocationResourceTags = ipamResourceTags(keyvaluetags.New(v.(map[string]interface{})))
	}

	if v, ok := d.GetOk("auto_import"); ok {
		input.AutoImport = aws.Bool(v.(bool))
	}

	if v, ok := d.GetOk("description"); ok {
		input.Description = aws.String(v.(string))
	}

	if v, ok := d.GetOk("locale"); ok && v != "None" {
		input.Locale = aws.String(v.(string))
	}

	if v, ok := d.GetOk("source_ipam_pool_id"); ok {
		input.SourceIpamPoolId = aws.String(v.(string))
	}

	log.Printf("[DEBUG] Creating IPAM Pool: %s", input)
	output, err := conn.CreateIpamPool(input)
	if err != nil {
		return fmt.Errorf("Error creating ipam pool in ipam scope (%s): %w", d.Get("ipam_scope_id").(string), err)
	}
	d.SetId(aws.StringValue(output.IpamPool.IpamPoolId))
	log.Printf("[INFO] IPAM Pool ID: %s", d.Id())

	if _, err = waitIpamPoolAvailable(conn, d.Id(), IpamPoolCreateTimeout); err != nil {
		return fmt.Errorf("error waiting for IPAM Pool (%s) to be Available: %w", d.Id(), err)
	}

	return resourceAwsVpcIpamPoolRead(d, meta)
}

func resourceAwsVpcIpamPoolRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn
	// defaultTagsConfig := meta.(*AWSClient).DefaultTagsConfig
	// tags := defaultTagsConfig.MergeTags(keyvaluetags.New(d.Get("tags").(map[string]interface{})))

	pool, err := findIpamPoolById(conn, d.Id())

	if err != nil && !tfawserr.ErrCodeEquals(err, InvalidIpamPoolIdNotFound) {
		return err
	}

	if !d.IsNewResource() && pool == nil {
		log.Printf("[WARN] IPAM Pool (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	d.Set("arn", pool.IpamPoolArn)

	scopeId := strings.Split(*pool.IpamScopeArn, "/")[1]

	d.Set("address_family", pool.AddressFamily)

	if pool.Advertisable != nil {
		d.Set("advertisable", pool.Advertisable)
	}

	d.Set("allocation_resource_tags", keyvaluetags.Ec2KeyValueTags(ec2TagsFromIpamAllocationTags(pool.AllocationResourceTags)).Map())
	d.Set("auto_import", pool.AutoImport)
	d.Set("description", pool.Description)
	d.Set("ipam_scope_id", scopeId)
	d.Set("ipam_scope_type", pool.IpamScopeType)
	d.Set("locale", pool.Locale)
	d.Set("pool_depth", pool.PoolDepth)
	d.Set("source_ipam_pool_id", pool.SourceIpamPoolId)
	d.Set("state", pool.State)

	return nil
}

func resourceAwsVpcIpamPoolUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	// TODO: rm StorageGateway
	// if d.HasChange("tags_all") {
	// 	o, n := d.GetChange("tags_all")
	// 	if err := keyvaluetags.StoragegatewayUpdateTags(conn, d.Get("arn").(string), o, n); err != nil {
	// 		return fmt.Errorf("error updating tags: %w", err)
	// 	}
	// }}

	input := &ec2.ModifyIpamPoolInput{
		IpamPoolId: aws.String(d.Id()),
	}

	if d.HasChangesExcept("tags_all", "allocation_resource_tags") {
		if v, ok := d.GetOk("allocation_default_netmask_length"); ok {
			input.AllocationDefaultNetmaskLength = aws.Int64(int64(v.(int)))
		}

		if v, ok := d.GetOk("auto_import"); ok {
			input.AutoImport = aws.Bool(v.(bool))
		}

		if v, ok := d.GetOk("allocation_max_netmask_length"); ok {
			input.AllocationMaxNetmaskLength = aws.Int64(int64(v.(int)))
		}

		if v, ok := d.GetOk("allocation_min_netmask_length"); ok {
			input.AllocationMinNetmaskLength = aws.Int64(int64(v.(int)))
		}

		// if v, ok := d.GetOk("allocation_tags"); ok && len(v.(map[string]interface{})) > 0 {
		// 	input.AllocationResourceTags = keyvaluetags.New(v.(map[string]interface{})).Ec2Tags()
		// }

		if v, ok := d.GetOk("description"); ok {
			input.Description = aws.String(v.(string))
		}
	}

	if d.HasChange("allocation_resource_tags") {
		o, n := d.GetChange("allocation_resource_tags")
		oldTags := keyvaluetags.New(o)
		newTags := keyvaluetags.New(n)

		if removedTags := oldTags.Removed(newTags); len(removedTags) > 0 {
			input.RemoveAllocationResourceTags = ipamResourceTags(removedTags.IgnoreAws())
		}

		if updatedTags := oldTags.Updated(newTags); len(updatedTags) > 0 {
			input.AddAllocationResourceTags = ipamResourceTags(updatedTags.IgnoreAws())
			//updatedTags.IgnoreAws().IpamResourceTags()
		}
	}

	log.Printf("[DEBUG] Updating IPAM pool: %s", input)
	_, err := conn.ModifyIpamPool(input)
	if err != nil {
		return fmt.Errorf("error updating IPAM Pool (%s): %w", d.Id(), err)
	}

	if _, err = waitIpamPoolUpdate(conn, d.Id(), IpamPoolUpdateTimeout); err != nil {
		return fmt.Errorf("error waiting for IPAM Pool (%s) to be Available: %w", d.Id(), err)
	}

	return resourceAwsVpcIpamPoolRead(d, meta)
}

func resourceAwsVpcIpamPoolDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	input := &ec2.DeleteIpamPoolInput{
		IpamPoolId: aws.String(d.Id()),
	}

	log.Printf("[DEBUG] Deleting IPAM Pool: %s", input)
	_, err := conn.DeleteIpamPool(input)
	if err != nil {
		return fmt.Errorf("error deleting IPAM Pool: (%s): %w", d.Id(), err)
	}

	if _, err = waitIpamPoolDeleted(conn, d.Id(), IpamPoolDeleteTimeout); err != nil {
		if isResourceNotFoundError(err) {
			return nil
		}
		return fmt.Errorf("error waiting for IPAM Pool (%s) to be deleted: %w", d.Id(), err)
	}

	return nil
}

func findIpamPoolById(conn *ec2.EC2, id string) (*ec2.IpamPool, error) {
	input := &ec2.DescribeIpamPoolsInput{
		IpamPoolIds: aws.StringSlice([]string{id}),
	}

	output, err := conn.DescribeIpamPools(input)

	if err != nil {
		return nil, err
	}

	if output == nil || len(output.IpamPools) == 0 || output.IpamPools[0] == nil {
		return nil, nil
	}

	return output.IpamPools[0], nil
}

func waitIpamPoolAvailable(conn *ec2.EC2, ipamPoolId string, timeout time.Duration) (*ec2.IpamPool, error) {
	stateConf := &resource.StateChangeConf{
		Pending: []string{ec2.IpamPoolStateCreateInProgress},
		Target:  []string{ec2.IpamPoolStateCreateComplete},
		Refresh: statusIpamPoolStatus(conn, ipamPoolId),
		Timeout: timeout,
		Delay:   IpamPoolAvailableDelay,
	}

	outputRaw, err := stateConf.WaitForState()

	if output, ok := outputRaw.(*ec2.IpamPool); ok {
		return output, err
	}

	return nil, err
}

func waitIpamPoolUpdate(conn *ec2.EC2, ipamPoolId string, timeout time.Duration) (*ec2.IpamPool, error) {
	stateConf := &resource.StateChangeConf{
		Pending: []string{ec2.IpamPoolStateModifyInProgress},
		Target:  []string{ec2.IpamPoolStateModifyComplete},
		Refresh: statusIpamPoolStatus(conn, ipamPoolId),
		Timeout: timeout,
		Delay:   IpamPoolAvailableDelay,
	}

	outputRaw, err := stateConf.WaitForState()

	if output, ok := outputRaw.(*ec2.IpamPool); ok {
		return output, err
	}

	return nil, err
}

func waitIpamPoolDeleted(conn *ec2.EC2, ipamPoolId string, timeout time.Duration) (*ec2.IpamPool, error) {
	stateConf := &resource.StateChangeConf{
		Pending: []string{ec2.IpamPoolStateDeleteInProgress},
		Target:  []string{InvalidIpamPoolIdNotFound},
		Refresh: statusIpamPoolStatus(conn, ipamPoolId),
		Timeout: timeout,
		Delay:   IpamPoolDeleteDelay,
	}

	outputRaw, err := stateConf.WaitForState()

	if output, ok := outputRaw.(*ec2.IpamPool); ok {
		return output, err
	}

	return nil, err
}

func statusIpamPoolStatus(conn *ec2.EC2, ipamPoolId string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {

		output, err := findIpamPoolById(conn, ipamPoolId)

		if tfawserr.ErrCodeEquals(err, InvalidIpamPoolIdNotFound) {
			return output, InvalidIpamPoolIdNotFound, nil
		}

		// there was an unhandled error in the Finder
		if err != nil {
			return nil, "", err
		}

		return output, aws.StringValue(output.State), nil
	}
}

func ipamResourceTags(tags keyvaluetags.KeyValueTags) []*ec2.RequestIpamResourceTag {
	result := make([]*ec2.RequestIpamResourceTag, 0, len(tags))

	for k, v := range tags.Map() {
		tag := &ec2.RequestIpamResourceTag{
			Key:   aws.String(k),
			Value: aws.String(v),
		}

		result = append(result, tag)
	}

	return result
}

func ec2TagsFromIpamAllocationTags(rts []*ec2.IpamResourceTag) []*ec2.Tag {
	if len(rts) == 0 {
		return nil
	}

	tags := []*ec2.Tag{}
	for _, ts := range rts {
		tags = append(tags, &ec2.Tag{
			Key:   ts.Key,
			Value: ts.Value,
		})
	}

	return tags
}
