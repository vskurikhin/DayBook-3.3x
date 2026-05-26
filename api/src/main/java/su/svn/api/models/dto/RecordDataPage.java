package su.svn.api.models.dto;

import java.util.List;

public class RecordDataPage extends Page<RecordData> {
    public RecordDataPage(List<RecordData> list, long pageCount, long pageIndex, long pageSize) {
        super(list, pageCount, pageIndex, pageSize);
    }
}
