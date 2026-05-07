# PagedModelEntityModelResourceRecordView


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**embedded** | [**PagedModelEntityModelResourceRecordViewEmbedded**](PagedModelEntityModelResourceRecordViewEmbedded.md) |  | [optional] 
**links** | [**Dict[str, Link]**](Link.md) |  | [optional] 
**page** | [**PageMetadata**](PageMetadata.md) |  | [optional] 

## Example

```python
from core.model.paged_model_entity_model_resource_record_view import PagedModelEntityModelResourceRecordView

# TODO update the JSON string below
json = "{}"
# create an instance of PagedModelEntityModelResourceRecordView from a JSON string
paged_model_entity_model_resource_record_view_instance = PagedModelEntityModelResourceRecordView.from_json(json)
# print the JSON string representation of the object
print(PagedModelEntityModelResourceRecordView.to_json())

# convert the object into a dict
paged_model_entity_model_resource_record_view_dict = paged_model_entity_model_resource_record_view_instance.to_dict()
# create an instance of PagedModelEntityModelResourceRecordView from a dict
paged_model_entity_model_resource_record_view_from_dict = PagedModelEntityModelResourceRecordView.from_dict(paged_model_entity_model_resource_record_view_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


