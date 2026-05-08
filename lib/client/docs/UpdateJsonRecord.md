# UpdateJsonRecord


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**id** | **UUID** |  | 
**parent_id** | **UUID** |  | [default to '00000000-0000-0000-0000-000000000000']
**title** | **str** |  | [optional] 
**var_json** | **Dict[str, str]** |  | [optional] 
**post_at** | **datetime** |  | [optional] 
**refresh_at** | **datetime** |  | 
**visible** | **bool** |  | [optional] 
**flags** | **int** |  | [optional] 
**tags** | **List[str]** |  | [optional] 

## Example

```python
from core.model.update_json_record import UpdateJsonRecord

# TODO update the JSON string below
json = "{}"
# create an instance of UpdateJsonRecord from a JSON string
update_json_record_instance = UpdateJsonRecord.from_json(json)
# print the JSON string representation of the object
print(UpdateJsonRecord.to_json())

# convert the object into a dict
update_json_record_dict = update_json_record_instance.to_dict()
# create an instance of UpdateJsonRecord from a dict
update_json_record_from_dict = UpdateJsonRecord.from_dict(update_json_record_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


