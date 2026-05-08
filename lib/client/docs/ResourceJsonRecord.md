# ResourceJsonRecord


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**id** | **UUID** |  | [optional] 
**visible** | **bool** |  | [optional] 
**flags** | **int** |  | [optional] 
**parent_id** | **UUID** |  | [optional] 
**title** | **str** |  | [optional] 
**var_json** | **Dict[str, str]** |  | [optional] 
**post_at** | **datetime** |  | [optional] 
**refresh_at** | **datetime** |  | [optional] 
**tags** | **List[str]** |  | [optional] 

## Example

```python
from core.model.resource_json_record import ResourceJsonRecord

# TODO update the JSON string below
json = "{}"
# create an instance of ResourceJsonRecord from a JSON string
resource_json_record_instance = ResourceJsonRecord.from_json(json)
# print the JSON string representation of the object
print(ResourceJsonRecord.to_json())

# convert the object into a dict
resource_json_record_dict = resource_json_record_instance.to_dict()
# create an instance of ResourceJsonRecord from a dict
resource_json_record_from_dict = ResourceJsonRecord.from_dict(resource_json_record_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


