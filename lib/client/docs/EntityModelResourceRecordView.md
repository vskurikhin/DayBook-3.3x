# EntityModelResourceRecordView


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**id** | **UUID** |  | [optional] 
**visible** | **bool** |  | [optional] 
**flags** | **int** |  | [optional] 
**parent_id** | **UUID** |  | [optional] 
**user_name** | **str** |  | [optional] 
**post_at** | **datetime** |  | [optional] 
**refresh_at** | **datetime** |  | [optional] 
**last_changed_time** | **datetime** |  | [optional] 
**title** | **str** |  | [optional] 
**values** | **Dict[str, str]** |  | [optional] 
**links** | [**Dict[str, Link]**](Link.md) |  | [optional] 

## Example

```python
from core.model.entity_model_resource_record_view import EntityModelResourceRecordView

# TODO update the JSON string below
json = "{}"
# create an instance of EntityModelResourceRecordView from a JSON string
entity_model_resource_record_view_instance = EntityModelResourceRecordView.from_json(json)
# print the JSON string representation of the object
print(EntityModelResourceRecordView.to_json())

# convert the object into a dict
entity_model_resource_record_view_dict = entity_model_resource_record_view_instance.to_dict()
# create an instance of EntityModelResourceRecordView from a dict
entity_model_resource_record_view_from_dict = EntityModelResourceRecordView.from_dict(entity_model_resource_record_view_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


