# NewJsonRecord


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**visible** | **bool** |  | [optional] 
**flags** | **int** |  | [optional] 
**parent_id** | **UUID** |  | [default to '00000000-0000-0000-0000-000000000000']
**title** | **str** |  | [optional] 
**var_json** | **Dict[str, str]** |  | [optional] 
**post_at** | **datetime** |  | 
**tags** | **List[str]** |  | [optional] 

## Example

```python
from core.model.new_json_record import NewJsonRecord

# TODO update the JSON string below
json = "{}"
# create an instance of NewJsonRecord from a JSON string
new_json_record_instance = NewJsonRecord.from_json(json)
# print the JSON string representation of the object
print(NewJsonRecord.to_json())

# convert the object into a dict
new_json_record_dict = new_json_record_instance.to_dict()
# create an instance of NewJsonRecord from a dict
new_json_record_from_dict = NewJsonRecord.from_dict(new_json_record_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


