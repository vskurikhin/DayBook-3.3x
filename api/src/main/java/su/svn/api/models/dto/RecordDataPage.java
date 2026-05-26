package su.svn.api.models.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.quarkus.runtime.annotations.RegisterForReflection;

import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
@RegisterForReflection
public class RecordDataPage extends Page<RecordData> {
    public RecordDataPage(List<RecordData> list, long pageCount, long pageIndex, long pageSize) {
        super(list, pageCount, pageIndex, pageSize);
    }
}
