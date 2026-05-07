# ResourceRecordViewFilter


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**title** | **str** |  | [optional] 
**from_time** | **datetime** |  | [optional] 
**from_date** | **datetime** |  | [optional] 
**to_date** | **datetime** |  | [optional] 
**with_disabled** | **bool** |  | [optional] 

## Example

```python
from core.model.resource_record_view_filter import ResourceRecordViewFilter

# TODO update the JSON string below
json = "{}"
# create an instance of ResourceRecordViewFilter from a JSON string
resource_record_view_filter_instance = ResourceRecordViewFilter.from_json(json)
# print the JSON string representation of the object
print(ResourceRecordViewFilter.to_json())

# convert the object into a dict
resource_record_view_filter_dict = resource_record_view_filter_instance.to_dict()
# create an instance of ResourceRecordViewFilter from a dict
resource_record_view_filter_from_dict = ResourceRecordViewFilter.from_dict(resource_record_view_filter_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


