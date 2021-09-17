package aws

import (
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/aws-sdk-go-base/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	// "github.com/terraform-providers/terraform-provider-aws/aws/internal/keyvaluetags"
)

func resourceAwsVpcIpam() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsVpcIpamCreate,
		Read:   resourceAwsVpcIpamRead,
		Update: resourceAwsVpcIpamUpdate,
		Delete: resourceAwsVpcIpamDelete,
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
			"operating_regions": {
				Type:     schema.TypeSet,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"region_name": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validateRegionName,
						},
					},
				},
			},
			"private_default_scope_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"public_default_scope_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"scope_count": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			// "tags":     tagsSchema(),
			// "tags_all": tagsSchemaComputed(),
		},
	}
}

const (
	IpamStatusAvailable   = "Available"
	InvalidIpamIdNotFound = "InvalidIpamId.NotFound"
	IpamDeleteTimeout     = 3 * time.Minute
	IpamDeleteDelay       = 5 * time.Second
)

func resourceAwsVpcIpamCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn
	current_region := meta.(*AWSClient).region
	// defaultTagsConfig := meta.(*AWSClient).DefaultTagsConfig
	// tags := defaultTagsConfig.MergeTags(keyvaluetags.New(d.Get("tags").(map[string]interface{})))

	input := &ec2.CreateIpamInput{
		ClientToken: aws.String(resource.UniqueId()),
		// TagSpecifications: ec2TagSpecificationsFromKeyValueTags(tags, "ipam"),
	}

	if v, ok := d.GetOk("description"); ok {
		input.Description = aws.String(v.(string))
	}

	operatingRegions := d.Get("operating_regions").(*schema.Set).List()
	if !expandAwsIpamOperatingRegionsDefaultRegion(operatingRegions, current_region) {
		return fmt.Errorf("Must include (%s) as a operating_region", current_region)
	}
	input.OperatingRegions = expandAwsIpamOperatingRegions(operatingRegions)

	log.Printf("[DEBUG] Creating IPAM: %s", input)
	output, err := conn.CreateIpam(input)
	if err != nil {
		return fmt.Errorf("Error creating ipam: %w", err)
	}
	d.SetId(aws.StringValue(output.Ipam.IpamId))
	log.Printf("[INFO] IPAM ID: %s", d.Id())

	return resourceAwsVpcIpamRead(d, meta)
}

func resourceAwsVpcIpamRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn
	// defaultTagsConfig := meta.(*AWSClient).DefaultTagsConfig
	// ignoreTagsConfig := meta.(*AWSClient).IgnoreTagsConfig

	ipam, err := findIpamById(conn, d.Id())

	if err != nil && !tfawserr.ErrCodeEquals(err, InvalidIpamIdNotFound) {
		return err
	}

	if !d.IsNewResource() && ipam == nil {
		log.Printf("[WARN] IPAM (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	d.Set("arn", ipam.IpamArn)
	d.Set("description", ipam.Description)
	d.Set("operating_regions", flattenAwsIpamOperatingRegions(ipam.OperatingRegions))
	d.Set("public_default_scope_id", ipam.PublicDefaultScopeId)
	d.Set("private_default_scope_id", ipam.PrivateDefaultScopeId)
	d.Set("scope_count", aws.Int64Value(ipam.ScopeCount))

	// tags := keyvaluetags.Ec2KeyValueTags(ipam.Tags).IgnoreAws().IgnoreConfig(ignoreTagsConfig)

	// //lintignore:AWSR002
	// if err := d.Set("tags", tags.RemoveDefaultConfig(defaultTagsConfig).Map()); err != nil {
	// 	return fmt.Errorf("error setting tags: %w", err)
	// }

	// if err := d.Set("tags_all", tags.Map()); err != nil {
	// 	return fmt.Errorf("error setting tags_all: %w", err)
	// }

	return nil
}

func resourceAwsVpcIpamUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	// if d.HasChange("tags_all") {
	// 	o, n := d.GetChange("tags_all")
	// 	if err := keyvaluetags.Ec2UpdateTags(conn, d.Get("arn").(string), o, n); err != nil {
	// 		return fmt.Errorf("error updating tags: %w", err)
	// 	}
	// }
	input := &ec2.ModifyIpamInput{
		IpamId: aws.String(d.Id()),
	}

	if d.HasChange("description") {
		input.Description = aws.String(d.Get("description").(string))
	}

	if d.HasChange("operating_regions") {
		o, n := d.GetChange("operating_regions")
		if o == nil {
			o = new(schema.Set)
		}
		if n == nil {
			n = new(schema.Set)
		}

		os := o.(*schema.Set)
		ns := n.(*schema.Set)
		operatingRegionUpdateAdd := expandAwsIpamOperatingRegionsUpdateAddRegions(ns.Difference(os).List())
		operatingRegionUpdateRemove := expandAwsIpamOperatingRegionsUpdateDeleteRegions(os.Difference(ns).List())

		if len(operatingRegionUpdateAdd) != 0 {
			input.AddOperatingRegions = operatingRegionUpdateAdd
		}

		if len(operatingRegionUpdateRemove) != 0 {
			input.RemoveOperatingRegions = operatingRegionUpdateRemove
		}
		_, err := conn.ModifyIpam(input)
		if err != nil {
			return fmt.Errorf("Error modifying operating regions to ipam: %w", err)
		}
	}

	return nil
}

func resourceAwsVpcIpamDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	input := &ec2.DeleteIpamInput{
		IpamId: aws.String(d.Id()),
	}

	log.Printf("[DEBUG] Deleting IPAM: %s", input)
	_, err := conn.DeleteIpam(input)
	if err != nil {
		return fmt.Errorf("error deleting IPAM: (%s): %w", d.Id(), err)
	}

	if _, err = waiterIpamDeleted(conn, d.Id(), IpamDeleteTimeout); err != nil {
		if tfawserr.ErrCodeEquals(err, InvalidIpamIdNotFound) {
			return nil
		}
		return fmt.Errorf("error waiting for IPAM (%s) to be deleted: %w", d.Id(), err)
	}

	return nil
}

func findIpamById(conn *ec2.EC2, id string) (*ec2.Ipam, error) {
	input := &ec2.DescribeIpamsInput{
		IpamIds: aws.StringSlice([]string{id}),
	}

	output, err := conn.DescribeIpams(input)

	if err != nil {
		return nil, err
	}

	if output == nil || len(output.Ipams) == 0 || output.Ipams[0] == nil {
		return nil, nil
	}

	return output.Ipams[0], nil
}

func waiterIpamDeleted(conn *ec2.EC2, ipamId string, timeout time.Duration) (*ec2.Ipam, error) {
	stateConf := &resource.StateChangeConf{
		Pending: []string{IpamStatusAvailable},
		Target:  []string{InvalidIpamIdNotFound},
		Refresh: statusIpamStatus(conn, ipamId),
		Timeout: timeout,
		Delay:   IpamDeleteDelay,
	}

	outputRaw, err := stateConf.WaitForState()

	if output, ok := outputRaw.(*ec2.Ipam); ok {
		return output, err
	}

	return nil, err
}

func statusIpamStatus(conn *ec2.EC2, ipamId string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {

		output, err := findIpamById(conn, ipamId)

		if tfawserr.ErrCodeEquals(err, InvalidIpamIdNotFound) {
			return output, InvalidIpamIdNotFound, nil
		}

		// there was an unhandled error in the Finder
		if err != nil {
			return nil, "", err
		}

		return output, IpamStatusAvailable, nil
	}
}

func expandAwsIpamOperatingRegions(operatingRegions []interface{}) []*ec2.AddIpamOperatingRegion {
	regions := make([]*ec2.AddIpamOperatingRegion, 0, len(operatingRegions))
	for _, regionRaw := range operatingRegions {
		region := regionRaw.(map[string]interface{})
		regions = append(regions, expandAwsIpamOperatingRegion(region))
	}

	return regions
}

func expandAwsIpamOperatingRegion(operatingRegion map[string]interface{}) *ec2.AddIpamOperatingRegion {
	region := &ec2.AddIpamOperatingRegion{
		RegionName: aws.String(operatingRegion["region_name"].(string)),
	}
	return region
}

func flattenAwsIpamOperatingRegions(operatingRegions []*ec2.IpamOperatingRegion) []interface{} {
	regions := []interface{}{}
	for _, operatingRegion := range operatingRegions {
		regions = append(regions, flattenAwsIpamOperatingRegion(operatingRegion))
	}
	return regions
}

func flattenAwsIpamOperatingRegion(operatingRegion *ec2.IpamOperatingRegion) map[string]interface{} {
	region := make(map[string]interface{})
	region["region_name"] = *operatingRegion.RegionName
	return region
}

func expandAwsIpamOperatingRegionsUpdateAddRegions(operatingRegions []interface{}) []*ec2.AddIpamOperatingRegion {
	regionUpdates := make([]*ec2.AddIpamOperatingRegion, 0, len(operatingRegions))
	for _, regionRaw := range operatingRegions {
		region := regionRaw.(map[string]interface{})
		regionUpdates = append(regionUpdates, expandAwsIpamOperatingRegionsUpdateAddRegion(region))
	}
	return regionUpdates
}

func expandAwsIpamOperatingRegionsUpdateAddRegion(operatingRegion map[string]interface{}) *ec2.AddIpamOperatingRegion {
	regionUpdate := &ec2.AddIpamOperatingRegion{
		RegionName: aws.String(operatingRegion["region_name"].(string)),
	}
	return regionUpdate
}

func expandAwsIpamOperatingRegionsUpdateDeleteRegions(operatingRegions []interface{}) []*ec2.RemoveIpamOperatingRegion {
	regionUpdates := make([]*ec2.RemoveIpamOperatingRegion, 0, len(operatingRegions))
	for _, regionRaw := range operatingRegions {
		region := regionRaw.(map[string]interface{})
		regionUpdates = append(regionUpdates, expandAwsIpamOperatingRegionsUpdateDeleteRegion(region))
	}
	return regionUpdates
}

func expandAwsIpamOperatingRegionsUpdateDeleteRegion(operatingRegion map[string]interface{}) *ec2.RemoveIpamOperatingRegion {
	regionUpdate := &ec2.RemoveIpamOperatingRegion{
		RegionName: aws.String(operatingRegion["region_name"].(string)),
	}
	return regionUpdate
}

func expandAwsIpamOperatingRegionsDefaultRegion(operatingRegions []interface{}, current_region string) bool {
	for _, regionRaw := range operatingRegions {
		region := regionRaw.(map[string]interface{})
		if region["region_name"].(string) == current_region {
			return true
		}
	}
	return false
}
