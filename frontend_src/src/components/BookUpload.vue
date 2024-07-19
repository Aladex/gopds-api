<template>
  <v-row justify="center">
    <v-col
        class="mt-4"
        cols="12"
        lg="6"
        md="10"
        sm="10"
    >
      <v-row>
        <v-col
            class="text-center"
            cols="12"
        >
          <h1>Что дальше?</h1>
          <p>Тыкаем по форме, загружаем файл и ждем.</p>
          <p>Файл должен пройти премодерацию. Если все хорошо, то книга очень скоро появится в общем списке</p>
        </v-col>
      </v-row>
      <v-row justify="center">
        <v-col
            cols="6"
        >
          <v-file-input
              accept="image/*"
              label="File input"
              v-model=bookFile
              show-size
          ></v-file-input>
        </v-col>
        <v-col cols="2">
          <v-btn
              class="search-btn mt-3"
              :disabled="bookFile === null"
              @click="submitFile"
          >
            Загрузить
          </v-btn>
        </v-col>
      </v-row>
    </v-col>
  </v-row>
</template>

<script>
export default {
  name: "BookUpload",
  data() {
    return {
      bookFile: null
    }
  },
  methods: {
    submitFile() {
      const form_data = new FormData();
      form_data.append("file", this.bookFile);
      this.$http.post('http://localhost:8085/api/books/upload-book',
          form_data, {
            headers: {
              'Content-Type': 'multipart/form-data'
            }
          }
      ).then(function () {
        console.log('SUCCESS!!');
      })
      .catch(function () {
            console.log('FAILURE!!');
      });
    },
  }
}
</script>

<style scoped>

</style>