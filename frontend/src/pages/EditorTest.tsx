import CodeMirror from "@uiw/react-codemirror";
import { sql } from "@codemirror/lang-sql";
import { useState } from "react";

export default function EditorTest() {
  const [value, setValue] = useState("SELECT * FROM users;");

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <h1 className="text-2xl font-bold mb-4">CodeMirror SQL Test</h1>
      <div className="border rounded-md overflow-hidden">
        <CodeMirror
          value={value}
          height="300px"
          extensions={[sql()]}
          onChange={(val) => setValue(val)}
        />
      </div>
      <p className="mt-2 text-sm text-gray-500">
        Current value length: {value.length}
      </p>
    </div>
  );
}
